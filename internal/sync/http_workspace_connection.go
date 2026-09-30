package browsersync

import (
	"context"
	"errors"
	"time"
)

// Workspace-protocol (v2) connections watch only the workspaces their client asks for —
// normally the workspace it drives plus the shared workspace — instead of receiving the
// whole vault. The same connection still carries the account-wide
// credential log (v1 publish/events frames).
const syncMaxWatchedWorkspaces = 4

type workspaceWatch struct {
	workspace string
	after     int64
	remove    bool
}

func isWorkspaceFrame(kind string) bool {
	switch kind {
	case "publish_workspace", "claim", "watch_workspace", "unwatch_workspace", "slot_get", "blob_put", "blob_get", "records_pull", "records_push":
		return true
	}
	return false
}

func (s *BrowserSyncService) handleWorkspaceFrame(ctx context.Context, identity SyncConnectionIdentity, frame syncClientFrame, send func(any) bool, watches chan<- workspaceWatch) bool {
	fail := func(request, operation string, err error) bool {
		code, _ := syncErrorCode(err)
		return send(map[string]any{"type": "workspace_error", "request_id": request, "operation_id": operation, "code": code})
	}
	switch frame.Type {
	case "publish_workspace":
		op := frame.WorkspaceOp
		if op == nil || op.VaultID != identity.VaultID || op.DeviceID != identity.DeviceID {
			return fail(frame.RequestID, "", ErrSyncForbidden)
		}
		receipt, err := s.store.PublishBrowserSyncWorkspace(ctx, identity.UserID, *op)
		if err != nil {
			return fail(frame.RequestID, op.OperationID, err) && !errors.Is(err, ErrSyncForbidden)
		}
		return send(map[string]any{"type": "workspace_ack", "request_id": frame.RequestID, "receipt": receipt})
	case "claim":
		c := frame.Claim
		if c == nil || c.VaultID != identity.VaultID || c.DeviceID != identity.DeviceID {
			return fail(frame.RequestID, "", ErrSyncForbidden)
		}
		receipt, err := s.store.ClaimBrowserSyncWorkspace(ctx, identity.UserID, *c)
		if err != nil {
			return fail(frame.RequestID, c.OperationID, err) && !errors.Is(err, ErrSyncForbidden)
		}
		return send(map[string]any{"type": "workspace_ack", "request_id": frame.RequestID, "receipt": receipt})
	case "watch_workspace", "unwatch_workspace":
		if !validSyncID(frame.WorkspaceID) || frame.After < 0 || frame.After > SyncMaxCounter {
			return fail(frame.RequestID, "", ErrSyncInvalid)
		}
		select {
		case watches <- workspaceWatch{workspace: frame.WorkspaceID, after: frame.After, remove: frame.Type == "unwatch_workspace"}:
			return true
		default:
			return false
		}
	case "slot_get":
		slot, err := s.store.BrowserSyncSlot(ctx, identity.UserID, identity.VaultID, identity.DeviceID, frame.WorkspaceID, frame.ViewNodeID, frame.Slot)
		if err != nil {
			return fail(frame.RequestID, "", err)
		}
		return send(map[string]any{"type": "slot", "request_id": frame.RequestID, "workspace_id": frame.WorkspaceID, "view_node_id": frame.ViewNodeID, "slot_kind": frame.Slot, "slot": slot})
	case "blob_put":
		if frame.Blob == nil {
			return fail(frame.RequestID, "", ErrSyncInvalid)
		}
		if err := s.store.PutBrowserSyncBlob(ctx, identity.UserID, identity.VaultID, identity.DeviceID, *frame.Blob); err != nil {
			return fail(frame.RequestID, "", err)
		}
		return send(map[string]any{"type": "blob_ack", "request_id": frame.RequestID})
	case "blob_get":
		blobs, err := s.store.BrowserSyncBlobs(ctx, identity.UserID, identity.VaultID, identity.DeviceID, frame.BlobHashes)
		if err != nil {
			return fail(frame.RequestID, "", err)
		}
		return send(map[string]any{"type": "blobs", "request_id": frame.RequestID, "blobs": blobs})
	case "records_pull":
		if err := s.store.MarkBrowserSyncRecordsCapable(ctx, identity.UserID, identity.VaultID, identity.DeviceID); err != nil {
			return fail(frame.RequestID, "", err)
		}
		page, err := s.store.PullBrowserSyncRecords(ctx, identity.UserID, identity.VaultID, identity.DeviceID, frame.Collection, frame.After, 0)
		if err != nil {
			return fail(frame.RequestID, "", err)
		}
		return send(map[string]any{"type": "records", "request_id": frame.RequestID, "listing": page})
	case "records_push":
		results, cursor, err := s.store.PushBrowserSyncRecords(ctx, identity.UserID, identity.VaultID, identity.DeviceID, frame.Collection, frame.Writes)
		if err != nil {
			return fail(frame.RequestID, "", err)
		}
		return send(map[string]any{"type": "records_ack", "request_id": frame.RequestID, "collection": frame.Collection, "results": results, "cursor": cursor})
	default:
		return send(map[string]any{"type": "workspace_error", "code": "unknown_sync_frame"})
	}
}

// workspacePusher tracks the workspaces one connection watches and brings each up to
// date: a delta while the change feed covers the client's version,
// otherwise a full snapshot.
type workspacePusher struct {
	service  *BrowserSyncService
	identity SyncConnectionIdentity
	write    func(any) error
	watched  map[string]int64
}

func (p *workspacePusher) push(ctx context.Context, workspace string) error {
	_, err := p.pushed(ctx, workspace)
	return err
}

// pushed reports whether it sent the client anything.
func (p *workspacePusher) pushed(ctx context.Context, workspace string) (bool, error) {
	bounded, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	id := p.identity
	delta, err := p.service.workspaceDelta(bounded, id, workspace, p.watched[workspace])
	if errors.Is(err, ErrSyncWorkspaceSnapshot) || errors.Is(err, ErrSyncCursor) {
		snapshot, err := p.service.workspaceSnapshot(bounded, id, workspace)
		if err != nil {
			return false, err
		}
		p.watched[workspace] = snapshot.Version
		return true, p.write(map[string]any{"type": "workspace_snapshot", "snapshot": snapshot})
	}
	if err != nil {
		return false, err
	}
	if len(delta.Changes) == 0 {
		return false, nil
	}
	p.watched[workspace] = delta.Version
	return true, p.write(map[string]any{"type": "workspace_delta", "delta": delta})
}

func (p *workspacePusher) refresh(ctx context.Context) error {
	for workspace := range p.watched {
		if err := p.push(ctx, workspace); err != nil {
			return err
		}
	}
	return nil
}

// watch applies one watch request. A failure is reported to the client and
// never ends the connection.
func (p *workspacePusher) watch(ctx context.Context, w workspaceWatch) error {
	if w.remove {
		delete(p.watched, w.workspace)
		return nil
	}
	if _, ok := p.watched[w.workspace]; !ok && len(p.watched) >= syncMaxWatchedWorkspaces {
		return p.write(map[string]any{"type": "workspace_error", "code": "invalid_sync_request", "workspace_id": w.workspace})
	}
	p.watched[w.workspace] = w.after
	sent, err := p.pushed(ctx, w.workspace)
	if err != nil {
		code, _ := syncErrorCode(err)
		delete(p.watched, w.workspace)
		return p.write(map[string]any{"type": "workspace_error", "code": code, "workspace_id": w.workspace})
	}
	// A watch always answers: a client already at the workspace's version (a
	// brand-new workspace is version 0) learns it is current and may publish.
	if !sent {
		return p.write(map[string]any{"type": "workspace_current", "workspace_id": w.workspace, "version": p.watched[w.workspace]})
	}
	return nil
}

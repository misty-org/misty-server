package browsersync

import (
	"context"
	"errors"
	"time"
)

// Tree-protocol (v2) connections watch only the trees their client asks for —
// normally the tree it drives plus the shared tree — instead of receiving the
// whole workspace. The same connection still carries the account-wide
// credential log (v1 publish/events frames).
const syncMaxWatchedTrees = 4

type treeWatch struct {
	tree   string
	after  int64
	remove bool
}

func isTreeFrame(kind string) bool {
	switch kind {
	case "publish_tree", "claim", "watch_tree", "unwatch_tree", "slot_get", "blob_put", "blob_get":
		return true
	}
	return false
}

func (s *BrowserSyncService) handleTreeFrame(ctx context.Context, identity SyncConnectionIdentity, frame syncClientFrame, send func(any) bool, watches chan<- treeWatch) bool {
	fail := func(request, operation string, err error) bool {
		code, _ := syncErrorCode(err)
		return send(map[string]any{"type": "tree_error", "request_id": request, "operation_id": operation, "code": code})
	}
	switch frame.Type {
	case "publish_tree":
		op := frame.TreeOp
		if op == nil || op.WorkspaceID != identity.WorkspaceID || op.DeviceID != identity.DeviceID {
			return fail(frame.RequestID, "", ErrSyncForbidden)
		}
		receipt, err := s.store.PublishBrowserSyncTree(ctx, identity.UserID, *op)
		if err != nil {
			return fail(frame.RequestID, op.OperationID, err) && !errors.Is(err, ErrSyncForbidden)
		}
		return send(map[string]any{"type": "tree_ack", "request_id": frame.RequestID, "receipt": receipt})
	case "claim":
		c := frame.Claim
		if c == nil || c.WorkspaceID != identity.WorkspaceID || c.DeviceID != identity.DeviceID {
			return fail(frame.RequestID, "", ErrSyncForbidden)
		}
		receipt, err := s.store.ClaimBrowserSyncTree(ctx, identity.UserID, *c)
		if err != nil {
			return fail(frame.RequestID, c.OperationID, err) && !errors.Is(err, ErrSyncForbidden)
		}
		return send(map[string]any{"type": "tree_ack", "request_id": frame.RequestID, "receipt": receipt})
	case "watch_tree", "unwatch_tree":
		if !validSyncID(frame.TreeID) || frame.After < 0 || frame.After > SyncMaxCounter {
			return fail(frame.RequestID, "", ErrSyncInvalid)
		}
		select {
		case watches <- treeWatch{tree: frame.TreeID, after: frame.After, remove: frame.Type == "unwatch_tree"}:
			return true
		default:
			return false
		}
	case "slot_get":
		slot, err := s.store.BrowserSyncSlot(ctx, identity.UserID, identity.WorkspaceID, identity.DeviceID, frame.TreeID, frame.TabNodeID, frame.Slot)
		if err != nil {
			return fail(frame.RequestID, "", err)
		}
		return send(map[string]any{"type": "slot", "request_id": frame.RequestID, "tree_id": frame.TreeID, "tab_node_id": frame.TabNodeID, "slot_kind": frame.Slot, "slot": slot})
	case "blob_put":
		if frame.Blob == nil {
			return fail(frame.RequestID, "", ErrSyncInvalid)
		}
		if err := s.store.PutBrowserSyncBlob(ctx, identity.UserID, identity.WorkspaceID, identity.DeviceID, *frame.Blob); err != nil {
			return fail(frame.RequestID, "", err)
		}
		return send(map[string]any{"type": "blob_ack", "request_id": frame.RequestID})
	case "blob_get":
		blobs, err := s.store.BrowserSyncBlobs(ctx, identity.UserID, identity.WorkspaceID, identity.DeviceID, frame.BlobHashes)
		if err != nil {
			return fail(frame.RequestID, "", err)
		}
		return send(map[string]any{"type": "blobs", "request_id": frame.RequestID, "blobs": blobs})
	default:
		return send(map[string]any{"type": "tree_error", "code": "unknown_sync_frame"})
	}
}

// treePusher tracks the trees one connection watches and brings each up to
// date: a delta while the change feed covers the client's version,
// otherwise a full snapshot.
type treePusher struct {
	service  *BrowserSyncService
	identity SyncConnectionIdentity
	write    func(any) error
	watched  map[string]int64
}

func (p *treePusher) push(ctx context.Context, tree string) error {
	_, err := p.pushed(ctx, tree)
	return err
}

// pushed reports whether it sent the client anything.
func (p *treePusher) pushed(ctx context.Context, tree string) (bool, error) {
	bounded, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	id := p.identity
	delta, err := p.service.store.BrowserSyncTreeDelta(bounded, id.UserID, id.WorkspaceID, id.DeviceID, tree, p.watched[tree])
	if errors.Is(err, ErrSyncTreeSnapshot) || errors.Is(err, ErrSyncCursor) {
		snapshot, err := p.service.treeSnapshot(bounded, id, tree)
		if err != nil {
			return false, err
		}
		p.watched[tree] = snapshot.Version
		return true, p.write(map[string]any{"type": "tree_snapshot", "snapshot": snapshot})
	}
	if err != nil {
		return false, err
	}
	if len(delta.Changes) == 0 {
		return false, nil
	}
	p.watched[tree] = delta.Version
	return true, p.write(map[string]any{"type": "tree_delta", "delta": delta})
}

func (p *treePusher) refresh(ctx context.Context) error {
	for tree := range p.watched {
		if err := p.push(ctx, tree); err != nil {
			return err
		}
	}
	return nil
}

// watch applies one watch request. A failure is reported to the client and
// never ends the connection.
func (p *treePusher) watch(ctx context.Context, w treeWatch) error {
	if w.remove {
		delete(p.watched, w.tree)
		return nil
	}
	if _, ok := p.watched[w.tree]; !ok && len(p.watched) >= syncMaxWatchedTrees {
		return p.write(map[string]any{"type": "tree_error", "code": "invalid_sync_request", "tree_id": w.tree})
	}
	p.watched[w.tree] = w.after
	sent, err := p.pushed(ctx, w.tree)
	if err != nil {
		code, _ := syncErrorCode(err)
		delete(p.watched, w.tree)
		return p.write(map[string]any{"type": "tree_error", "code": code, "tree_id": w.tree})
	}
	// A watch always answers: a client already at the tree's version (a
	// brand-new tree is version 0) learns it is current and may publish.
	if !sent {
		return p.write(map[string]any{"type": "tree_current", "tree_id": w.tree, "version": p.watched[w.tree]})
	}
	return nil
}

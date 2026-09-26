package browsersync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func (s *BrowserSyncService) serveConnection(parent context.Context, conn *websocket.Conn, identity SyncConnectionIdentity, after int64, version int) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	connectionID := uuid.NewString()
	if s.store.BrowserSyncHeartbeat(ctx, identity, connectionID, after, false) != nil {
		return
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		_ = s.store.BrowserSyncDisconnect(cleanup, identity, connectionID)
		_ = s.store.NotifyBrowserSyncPresence(cleanup, identity)
	}()
	events, unsubscribe, err := s.database.SubscribeAccountEvents(ctx, identity.UserID)
	if err != nil {
		return
	}
	defer unsubscribe()
	_ = s.store.NotifyBrowserSyncPresence(ctx, identity)
	outgoing := make(chan any, 32)
	resume := make(chan int64, 1)
	var watches chan treeWatch
	if version == 2 {
		watches = make(chan treeWatch, 8)
	}
	done := make(chan struct{})
	send := func(frame any) bool {
		select {
		case outgoing <- frame:
			return true
		case <-ctx.Done():
			return false
		default:
			cancel()
			_ = conn.Close()
			return false
		}
	}
	go func() {
		defer close(done)
		defer cancel()
		defer conn.Close()
		s.writeConnection(ctx, conn, identity, connectionID, after, events, outgoing, resume, watches)
	}()
	defer func() { cancel(); _ = conn.Close(); <-done }()
	applied, ready := after, false
	activeEpoch := ""
	heartbeat := func() error {
		bounded, stop := context.WithTimeout(ctx, 5*time.Second)
		defer stop()
		if err := s.store.BrowserSyncHeartbeat(bounded, identity, connectionID, applied, ready, activeEpoch); err != nil {
			return err
		}
		if version == 2 {
			if err := s.store.TouchBrowserSyncDriver(bounded, identity); err != nil {
				return err
			}
		}
		return conn.SetReadDeadline(time.Now().Add(45 * time.Second))
	}
	conn.SetReadLimit(SyncTreeMaxOpBytes*4/3 + 64<<10)
	_ = conn.SetReadDeadline(time.Now().Add(45 * time.Second))
	conn.SetPongHandler(func(string) error { return heartbeat() })
	windowStart := time.Now()
	messages := 0
	for {
		kind, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if time.Since(windowStart) >= 5*time.Second {
			windowStart = time.Now()
			messages = 0
		}
		messages++
		if messages > 96 || kind != websocket.TextMessage {
			return
		}
		var frame syncClientFrame
		if err = decodeSync(bytes.NewReader(raw), &frame); err != nil {
			send(map[string]any{"type": "error", "code": "invalid_sync_frame"})
			continue
		}
		if version == 2 && isTreeFrame(frame.Type) {
			bounded, stop := context.WithTimeout(ctx, 10*time.Second)
			ok := s.handleTreeFrame(bounded, identity, frame, send, watches)
			stop()
			if !ok {
				return
			}
			continue
		}
		switch frame.Type {
		case "publish":
			if frame.Mutation == nil || frame.Mutation.WorkspaceID != identity.WorkspaceID || frame.Mutation.DeviceID != identity.DeviceID {
				send(map[string]any{"type": "error", "code": "sync_device_forbidden"})
				continue
			}
			bounded, stop := context.WithTimeout(ctx, 10*time.Second)
			receipt, err := s.store.PublishBrowserSync(bounded, identity.UserID, *frame.Mutation, SyncPublishOptions{ActiveEpoch: frame.ActiveEpoch})
			stop()
			if err != nil {
				code, _ := syncErrorCode(err)
				if !send(map[string]any{"type": "error", "operation_id": frame.Mutation.OperationID, "code": code}) {
					return
				}
				if errors.Is(err, ErrSyncForbidden) {
					return
				}
			} else if !send(map[string]any{"type": "ack", "receipt": receipt}) {
				return
			}
		case "heartbeat":
			if frame.AppliedSequence < applied || frame.AppliedSequence > SyncMaxCounter {
				send(map[string]any{"type": "error", "code": "sync_cursor_invalid"})
				continue
			}
			applied, ready = frame.AppliedSequence, frame.Ready
			activeEpoch = frame.ActiveEpoch
			if heartbeat() != nil {
				return
			}
			if frame.Activation != nil {
				m := frame.Activation
				if m.WorkspaceID != identity.WorkspaceID || m.DeviceID != identity.DeviceID {
					send(map[string]any{"type": "error", "code": "sync_device_forbidden"})
					continue
				}
				bounded, stop := context.WithTimeout(ctx, 10*time.Second)
				receipt, err := s.store.PublishBrowserSync(bounded, identity.UserID, *m, SyncPublishOptions{Activate: true})
				stop()
				if err != nil {
					code, _ := syncErrorCode(err)
					if !send(map[string]any{"type": "error", "operation_id": m.OperationID, "code": code}) {
						return
					}
				} else if !send(map[string]any{"type": "ack", "receipt": receipt}) {
					return
				}
			}
		case "resume":
			if frame.After < 0 || frame.After > SyncMaxCounter {
				send(map[string]any{"type": "error", "code": "sync_cursor_invalid"})
				continue
			}
			select {
			case resume <- frame.After:
			case <-ctx.Done():
				return
			default:
				return
			}
		default:
			if !send(map[string]any{"type": "error", "code": "unknown_sync_frame"}) {
				return
			}
		}
	}
}

func (s *BrowserSyncService) writeConnection(ctx context.Context, conn *websocket.Conn, identity SyncConnectionIdentity, connectionID string, cursor int64, events <-chan db.AccountEvent, outgoing <-chan any, resume <-chan int64, watches <-chan treeWatch) {
	write := func(value any) error {
		if err := conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
			return err
		}
		return conn.WriteJSON(value)
	}
	workspace, err := s.store.BrowserSyncWorkspace(ctx, identity.UserID)
	if err != nil || workspace == nil {
		return
	}
	welcome := map[string]any{"type": "welcome", "workspace": workspace, "connection_id": connectionID}
	if watches != nil {
		welcome["protocol_version"] = 2
	}
	if write(welcome) != nil {
		return
	}
	trees := &treePusher{service: s, identity: identity, write: write, watched: map[string]int64{}}
	var lastTrees []byte
	var lastDevices, lastPresence []byte
	presence := func() error {
		bounded, stop := context.WithTimeout(ctx, 5*time.Second)
		defer stop()
		devices, err := s.store.BrowserSyncDevices(bounded, identity.UserID, identity.WorkspaceID)
		if err != nil {
			return err
		}
		allowed := false
		for _, d := range devices {
			if d.DeviceID == identity.DeviceID && d.RevokedAt == nil {
				allowed = true
			}
		}
		if !allowed {
			return ErrSyncForbidden
		}
		raw, _ := json.Marshal(devices)
		if !bytes.Equal(raw, lastDevices) {
			if err = write(map[string]any{"type": "devices", "devices": devices}); err != nil {
				return err
			}
			lastDevices = raw
		}
		online, err := s.store.BrowserSyncPresence(bounded, identity.UserID, identity.WorkspaceID)
		if err != nil {
			return err
		}
		raw, _ = json.Marshal(online)
		if !bytes.Equal(raw, lastPresence) {
			if err = write(map[string]any{"type": "presence", "devices": online}); err != nil {
				return err
			}
			lastPresence = raw
		}
		if watches == nil {
			return nil
		}
		roster, err := s.store.BrowserSyncTrees(bounded, identity.UserID, identity.WorkspaceID)
		if err != nil {
			return err
		}
		raw, _ = json.Marshal(roster)
		if !bytes.Equal(raw, lastTrees) {
			if err = write(map[string]any{"type": "trees", "trees": roster}); err != nil {
				return err
			}
			lastTrees = raw
		}
		return nil
	}
	blocked := false
	replay := func() error {
		if blocked {
			return nil
		}
		if err := presence(); err != nil {
			return err
		}
		for {
			bounded, stop := context.WithTimeout(ctx, 5*time.Second)
			page, err := s.store.ReplayBrowserSync(bounded, identity.UserID, identity.WorkspaceID, identity.DeviceID, cursor, 200)
			stop()
			if err != nil {
				return err
			}
			if page.CheckpointRequired {
				blocked = true
				return write(map[string]any{"type": "checkpoint_required", "head_sequence": page.HeadSequence})
			}
			if len(page.Events) == 0 {
				return nil
			}
			if err = write(map[string]any{"type": "events", "replay": page}); err != nil {
				return err
			}
			cursor = page.Events[len(page.Events)-1].Sequence
			if cursor >= page.HeadSequence {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
		}
	}
	if replay() != nil {
		return
	}
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	// Hints can be dropped. Bounded reconciliation makes durability independent
	// of LISTEN delivery or another server instance's in-memory subscriber state.
	reconcile := time.NewTicker(5 * time.Second)
	defer reconcile.Stop()
	expiry := time.NewTimer(10 * time.Minute)
	defer expiry.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-expiry.C:
			closeBrowserSyncForReconnect(conn)
			return
		case next := <-resume:
			cursor = next
			blocked = false
			if replay() != nil {
				return
			}
		case frame := <-outgoing:
			if write(frame) != nil {
				return
			}
		case w := <-watches:
			if trees.watch(ctx, w) != nil {
				return
			}
		case event := <-events:
			if event.Topic == "reset" || event.Topic == "browser-sync" {
				if replay() != nil || trees.refresh(ctx) != nil {
					return
				}
			}
			if event.Topic == "browser-presence" {
				if presence() != nil {
					return
				}
			}
			if event.Topic != "browser-sync" && event.Topic != "browser-presence" {
				event.UserID = ""
				if write(map[string]any{"type": "account_event", "event": event}) != nil {
					return
				}
			}
		case <-reconcile.C:
			if replay() != nil || trees.refresh(ctx) != nil {
				return
			}
			if blocked && presence() != nil {
				return
			}
		case <-ping.C:
			if conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)) != nil {
				return
			}
		}
	}
}

package browsersync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/kannachi323/misty/server/internal/platform/metrics"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func (s *BrowserSyncService) serveConnection(parent context.Context, conn *websocket.Conn, identity SyncConnectionIdentity, after int64, version int) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	// A fenced process closes every socket, including this one.
	defer context.AfterFunc(s.connectionScope(), cancel)()
	connectionID := uuid.NewString()
	if s.store.BrowserSyncConnect(ctx, identity, connectionID, s.instanceID, after, false) != nil {
		return
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		_ = s.store.BrowserSyncDisconnect(cleanup, identity, connectionID)
	}()
	events, unsubscribe, err := s.database.SubscribeAccountEvents(ctx, identity.UserID)
	if err != nil {
		return
	}
	defer unsubscribe()
	outgoing := make(chan any, 32)
	resume := make(chan int64, 1)
	var watches chan workspaceWatch
	if version == syncWorkspaceProtocol {
		watches = make(chan workspaceWatch, 8)
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
	// Liveness is the socket itself (pings and the process lease). The database
	// hears only real progress: a changed applied cursor or readiness.
	applied, ready := after, false
	persistedApplied, persistedReady, caughtUp := after, false, false
	alive := func() error { return conn.SetReadDeadline(time.Now().Add(45 * time.Second)) }
	heartbeat := func() error {
		if applied != persistedApplied || ready != persistedReady {
			bounded, stop := context.WithTimeout(ctx, 5*time.Second)
			current, err := s.store.BrowserSyncProgress(bounded, identity, connectionID, applied, ready, caughtUp)
			stop()
			if err != nil {
				return err
			}
			persistedApplied, persistedReady, caughtUp = applied, ready, current
		}
		return alive()
	}
	conn.SetReadLimit(SyncWorkspaceMaxOpBytes*4/3 + 64<<10)
	_ = conn.SetReadDeadline(time.Now().Add(45 * time.Second))
	conn.SetPongHandler(func(payload string) error {
		metrics.RecordSyncMessage(ctx, "in", "pong", len(payload))
		return alive()
	})
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
		err = decodeSync(bytes.NewReader(raw), &frame)
		metrics.RecordSyncMessage(ctx, "in", frame.Type, len(raw))
		if err != nil {
			send(map[string]any{"type": "error", "code": "invalid_sync_frame"})
			continue
		}
		if version == syncWorkspaceProtocol && isWorkspaceFrame(frame.Type) {
			bounded, stop := context.WithTimeout(ctx, 10*time.Second)
			ok := s.handleWorkspaceFrame(bounded, identity, frame, send, watches)
			stop()
			if !ok {
				return
			}
			continue
		}
		switch frame.Type {
		case "publish":
			if frame.Mutation == nil || frame.Mutation.VaultID != identity.VaultID || frame.Mutation.DeviceID != identity.DeviceID {
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
			if heartbeat() != nil {
				return
			}
			if frame.Activation != nil {
				m := frame.Activation
				if m.VaultID != identity.VaultID || m.DeviceID != identity.DeviceID {
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

func (s *BrowserSyncService) writeConnection(ctx context.Context, conn *websocket.Conn, identity SyncConnectionIdentity, connectionID string, cursor int64, events <-chan db.AccountEvent, outgoing <-chan any, resume <-chan int64, watches <-chan workspaceWatch) {
	write := func(value any) error {
		if err := conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
			return err
		}
		return writeSyncJSON(ctx, conn, value)
	}
	vault, err := s.store.BrowserSyncVault(ctx, identity.UserID)
	if err != nil || vault == nil {
		return
	}
	welcome := map[string]any{"type": "welcome", "vault": vault, "connection_id": connectionID}
	if watches != nil {
		welcome["protocol_version"] = syncWorkspaceProtocol
	}
	if write(welcome) != nil {
		return
	}
	workspaces := &workspacePusher{service: s, identity: identity, write: write, watched: map[string]int64{}}
	var lastWorkspaces []byte
	var lastDevices, lastPresence []byte
	presence := func() error {
		bounded, stop := context.WithTimeout(ctx, 5*time.Second)
		defer stop()
		devices, err := s.store.BrowserSyncDevices(bounded, identity.UserID, identity.VaultID)
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
		online, err := s.store.BrowserSyncPresence(bounded, identity.UserID, identity.VaultID)
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
		roster, err := s.store.BrowserSyncWorkspaces(bounded, identity.UserID, identity.VaultID)
		if err != nil {
			return err
		}
		raw, _ = json.Marshal(roster)
		if !bytes.Equal(raw, lastWorkspaces) {
			if err = write(map[string]any{"type": "workspaces", "workspaces": roster}); err != nil {
				return err
			}
			lastWorkspaces = raw
		}
		return nil
	}
	blocked := false
	replay := func() error {
		if blocked {
			// A client awaiting a checkpoint still follows device changes.
			return presence()
		}
		if err := presence(); err != nil {
			return err
		}
		for {
			bounded, stop := context.WithTimeout(ctx, 5*time.Second)
			page, err := s.store.ReplayBrowserSync(bounded, identity.UserID, identity.VaultID, identity.DeviceID, cursor, 200)
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
	// Transport keepalive only: pings keep proxies and NAT mappings open and
	// detect dead peers through the read deadline. They never touch the database.
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	// Changes arrive by push. The account event hub turns a lost LISTEN session
	// or a slow consumer into a "reset", which replays authoritatively, so no
	// connection polls the database on a timer.
	//
	// The socket outlives the access token that minted its ticket. Instead of
	// reconnecting to re-present a credential, it confirms in band that the
	// minting account session is still active, on the same bound as before.
	revalidate := time.NewTimer(jittered(s.sessionRevalidation(), 0.25))
	defer revalidate.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-revalidate.C:
			if identity.SessionHash == "" {
				// Tickets from before session binding renew by reconnecting.
				closeBrowserSyncForReconnect(conn)
				return
			}
			bounded, stop := context.WithTimeout(ctx, 5*time.Second)
			active, err := s.database.AccountSessionActive(bounded, identity.SessionHash, identity.UserID)
			stop()
			if err == nil && !active {
				closeBrowserSyncForReconnect(conn)
				return
			}
			next := jittered(s.sessionRevalidation(), 0.25)
			if err != nil {
				// A database outage must not disconnect every device at once.
				next = jittered(min(time.Minute, s.sessionRevalidation()), 0.5)
			}
			revalidate.Reset(next)
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
			if workspaces.watch(ctx, w) != nil {
				return
			}
		case event := <-events:
			if event.Topic == "reset" || event.Topic == "browser-sync" {
				if replay() != nil || workspaces.refresh(ctx) != nil {
					return
				}
			}
			if event.Topic == "browser-presence" {
				if presence() != nil {
					return
				}
			}
			// Every other topic, including "browser-records" (a cold collection
			// moved; the client pulls it when it wants), is a content-free hint
			// that older clients safely ignore.
			if event.Topic != "browser-sync" && event.Topic != "browser-presence" {
				event.UserID = ""
				if write(map[string]any{"type": "account_event", "event": event}) != nil {
					return
				}
			}
		case <-ping.C:
			if conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)) != nil {
				return
			}
			metrics.RecordSyncMessage(ctx, "out", "ping", 0)
		}
	}
}

// jittered spreads d uniformly across ±spread of itself.
func jittered(d time.Duration, spread float64) time.Duration {
	return time.Duration(float64(d) * (1 - spread + 2*spread*rand.Float64()))
}

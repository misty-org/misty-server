package browsersync

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kannachi323/misty/server/internal/platform/metrics"
)

// syncReader is the inbound half of one browser sync socket. It owns the
// progress the client reports and decides what reaches the database.
type syncReader struct {
	service      *BrowserSyncService
	ctx          context.Context
	conn         *websocket.Conn
	identity     SyncConnectionIdentity
	connectionID string
	version      int
	batching     bool
	send         func(any) bool
	resume       chan<- int64
	watches      chan workspaceWatch

	applied, persistedApplied int64
	ready, persistedReady     bool
	caughtUp                  bool
}

// newSyncReader starts reading from the client's last applied cursor.
func newSyncReader(ctx context.Context, s *BrowserSyncService, conn *websocket.Conn, identity SyncConnectionIdentity, connectionID string, after int64, version int, batching bool, send func(any) bool, resume chan<- int64, watches chan workspaceWatch) *syncReader {
	return &syncReader{
		service:          s,
		ctx:              ctx,
		conn:             conn,
		identity:         identity,
		connectionID:     connectionID,
		version:          version,
		batching:         batching,
		send:             send,
		resume:           resume,
		watches:          watches,
		applied:          after,
		persistedApplied: after,
	}
}

// start sets the read limit, the first read deadline and the pong handler.
func (r *syncReader) start() {
	// Liveness is the socket itself (pings and the process lease). The database
	// hears only real progress: a changed applied cursor or readiness.
	r.conn.SetReadLimit(SyncWorkspaceMaxOpBytes*4/3 + 64<<10)
	_ = r.conn.SetReadDeadline(time.Now().Add(45 * time.Second))
	r.conn.SetPongHandler(func(payload string) error {
		metrics.RecordSyncMessage(r.ctx, "in", "pong", len(payload))
		return r.alive()
	})
}

func (r *syncReader) alive() error {
	return r.conn.SetReadDeadline(time.Now().Add(45 * time.Second))
}

// heartbeat persists progress only when it changed, then extends the read deadline.
func (r *syncReader) heartbeat() error {
	if r.applied != r.persistedApplied || r.ready != r.persistedReady {
		bounded, stop := context.WithTimeout(r.ctx, 5*time.Second)
		current, err := r.service.store.BrowserSyncProgress(bounded, r.identity, r.connectionID, r.applied, r.ready, r.caughtUp)
		stop()
		if err != nil {
			return err
		}
		r.persistedApplied, r.persistedReady, r.caughtUp = r.applied, r.ready, current
	}
	return r.alive()
}

// run reads frames until the socket fails or the client breaks the protocol.
func (r *syncReader) run() {
	windowStart := time.Now()
	messages := 0
	for {
		kind, raw, err := r.conn.ReadMessage()
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
		if !r.handleRaw(raw) {
			return
		}
	}
}

// handleRaw decodes one text frame and routes it. It returns false when the
// connection must stop reading.
func (r *syncReader) handleRaw(raw []byte) bool {
	var frame syncClientFrame
	err := decodeSync(bytes.NewReader(raw), &frame)
	metrics.RecordSyncMessage(r.ctx, "in", frame.Type, len(raw))
	if err != nil {
		r.send(map[string]any{"type": "error", "code": "invalid_sync_frame"})
		return true
	}
	if r.version == syncWorkspaceProtocol && isWorkspaceFrame(frame.Type) {
		bounded, stop := context.WithTimeout(r.ctx, 10*time.Second)
		ok := r.service.handleWorkspaceFrame(bounded, r.identity, frame, r.send, r.watches, r.batching)
		stop()
		return ok
	}
	switch frame.Type {
	case "publish":
		return r.handlePublish(frame)
	case "heartbeat":
		return r.handleHeartbeat(frame)
	case "resume":
		return r.handleResume(frame)
	default:
		return r.send(map[string]any{"type": "error", "code": "unknown_sync_frame"})
	}
}

// handlePublish stores a mutation and acknowledges or rejects it.
func (r *syncReader) handlePublish(frame syncClientFrame) bool {
	if frame.Mutation == nil || frame.Mutation.VaultID != r.identity.VaultID || frame.Mutation.DeviceID != r.identity.DeviceID {
		r.send(map[string]any{"type": "error", "code": "sync_device_forbidden"})
		return true
	}
	bounded, stop := context.WithTimeout(r.ctx, 10*time.Second)
	receipt, err := r.service.store.PublishBrowserSync(bounded, r.identity.UserID, *frame.Mutation, SyncPublishOptions{ActiveEpoch: frame.ActiveEpoch})
	stop()
	if err != nil {
		code, _ := syncErrorCode(err)
		if !r.send(map[string]any{"type": "error", "operation_id": frame.Mutation.OperationID, "code": code}) {
			return false
		}
		if errors.Is(err, ErrSyncForbidden) {
			return false
		}
		return true
	}
	return r.send(map[string]any{"type": "ack", "receipt": receipt})
}

// handleHeartbeat records reported progress and, when present, an activation publish.
func (r *syncReader) handleHeartbeat(frame syncClientFrame) bool {
	if frame.AppliedSequence < r.applied || frame.AppliedSequence > SyncMaxCounter {
		r.send(map[string]any{"type": "error", "code": "sync_cursor_invalid"})
		return true
	}
	r.applied, r.ready = frame.AppliedSequence, frame.Ready
	if r.heartbeat() != nil {
		return false
	}
	if frame.Activation == nil {
		return true
	}
	m := frame.Activation
	if m.VaultID != r.identity.VaultID || m.DeviceID != r.identity.DeviceID {
		r.send(map[string]any{"type": "error", "code": "sync_device_forbidden"})
		return true
	}
	bounded, stop := context.WithTimeout(r.ctx, 10*time.Second)
	receipt, err := r.service.store.PublishBrowserSync(bounded, r.identity.UserID, *m, SyncPublishOptions{Activate: true})
	stop()
	if err != nil {
		code, _ := syncErrorCode(err)
		return r.send(map[string]any{"type": "error", "operation_id": m.OperationID, "code": code})
	}
	return r.send(map[string]any{"type": "ack", "receipt": receipt})
}

// handleResume hands the client's replay cursor to the writer without blocking.
func (r *syncReader) handleResume(frame syncClientFrame) bool {
	if frame.After < 0 || frame.After > SyncMaxCounter {
		r.send(map[string]any{"type": "error", "code": "sync_cursor_invalid"})
		return true
	}
	select {
	case r.resume <- frame.After:
		return true
	case <-r.ctx.Done():
		return false
	default:
		return false
	}
}

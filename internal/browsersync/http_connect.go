package browsersync

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/misty-org/misty-server/internal/platform/db"
	"github.com/misty-org/misty-server/internal/platform/metrics"
	"github.com/misty-org/misty-server/internal/platform/security"
)

// The socket carries small JSON frames. Pushes are bounded by SyncMaxWritesPerOp and the frame
// limit, and pulls by SyncPageBytes, so one exchange never exceeds a frame.
const (
	syncFrameLimit  = 2 << 20
	syncFramesPer5s = 96
)

func syncConnectionProof(vault, session, challenge string) []byte {
	raw, _ := json.Marshal([]any{"misty.sync.connect.v1", vault, session, challenge})
	return raw
}

var browserSyncUpgrader = websocket.Upgrader{ReadBufferSize: 4096, WriteBufferSize: 4096,
	// A one-use authenticated ticket AND a fresh signature from the session's key are required.
	// No cookie-only browser upgrade can join this stream.
	CheckOrigin: func(*http.Request) bool { return true }, HandshakeTimeout: 10 * time.Second}

// syncClientFrame is one frame from a client. A type this version doesn't know is ignored, so a
// newer client never breaks an older server, or the other way round.
type syncClientFrame struct {
	Type      string            `json:"type"`
	RequestID string            `json:"request_id,omitempty"`
	Signature []byte            `json:"signature,omitempty"`
	After     int64             `json:"after,omitempty"`
	Writes    []SyncRecordWrite `json:"writes,omitempty"`
}

// Connect upgrades a ticket to a socket once the session proves it holds its key.
func (s *BrowserSyncService) Connect() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !websocket.IsWebSocketUpgrade(r) {
			w.Header().Set("Upgrade", "websocket")
			w.WriteHeader(426)
			return
		}
		token := r.URL.Query().Get("ticket")
		if len(token) < 32 || len(token) > 128 {
			writeSyncError(w, ErrSyncForbidden)
			return
		}
		identity, err := s.store.ConsumeTicket(r.Context(), security.HashToken(token))
		if err != nil {
			writeSyncError(w, err)
			return
		}
		conn, err := browserSyncUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		defer metrics.TrackSyncConnection(r.Context())()
		challenge, err := security.GenerateSecureToken()
		if err != nil {
			return
		}
		conn.SetReadLimit(4096)
		_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if writeSyncJSON(r.Context(), conn, map[string]any{"type": "challenge", "challenge": challenge}) != nil {
			return
		}
		kind, raw, err := conn.ReadMessage()
		if err != nil || kind != websocket.TextMessage {
			return
		}
		var auth syncClientFrame
		decodeErr := decodeSync(bytes.NewReader(raw), &auth)
		if decodeErr != nil || auth.Type != "authenticate" || len(identity.PublicKey) != ed25519.PublicKeySize ||
			!ed25519.Verify(identity.PublicKey, syncConnectionProof(identity.VaultID, identity.SessionID, challenge), auth.Signature) {
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "Session proof required"), time.Now().Add(time.Second))
			return
		}
		s.serve(r.Context(), conn, *identity)
	}
}

// jittered spreads d uniformly across ±spread of itself.
func jittered(d time.Duration, spread float64) time.Duration {
	return time.Duration(float64(d) * (1 - spread + 2*spread*rand.Float64()))
}

func (s *BrowserSyncService) serve(parent context.Context, conn *websocket.Conn, identity SyncConnectionIdentity) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	// Shutting down closes every socket.
	defer context.AfterFunc(s.connectionScope(), cancel)()
	_ = s.store.NoteSeen(ctx, identity)
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		_ = s.store.NoteSeen(cleanup, identity)
	}()
	events, unsubscribe, err := s.database.SubscribeAccountEvents(ctx, identity.UserID)
	if err != nil {
		return
	}
	defer unsubscribe()
	outgoing := make(chan any, 32)
	send := func(frame any) bool {
		select {
		case outgoing <- frame:
			return true
		case <-ctx.Done():
			return false
		default:
			// A client too slow to take its frames reconnects and pulls.
			cancel()
			_ = conn.Close()
			return false
		}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer cancel()
		defer conn.Close()
		s.writeLoop(ctx, conn, identity, events, outgoing)
	}()
	defer func() { cancel(); _ = conn.Close(); <-done }()
	s.readLoop(ctx, conn, identity, send)
}

func (s *BrowserSyncService) readLoop(ctx context.Context, conn *websocket.Conn, identity SyncConnectionIdentity, send func(any) bool) {
	alive := func() error { return conn.SetReadDeadline(time.Now().Add(45 * time.Second)) }
	conn.SetReadLimit(syncFrameLimit)
	_ = alive()
	conn.SetPongHandler(func(payload string) error {
		metrics.RecordSyncMessage(ctx, "in", "pong", len(payload))
		return alive()
	})
	windowStart, frames := time.Now(), 0
	for {
		kind, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if time.Since(windowStart) >= 5*time.Second {
			windowStart, frames = time.Now(), 0
		}
		frames++
		if frames > syncFramesPer5s || kind != websocket.TextMessage {
			return
		}
		if alive() != nil {
			return
		}
		var frame syncClientFrame
		if err = decodeSync(bytes.NewReader(raw), &frame); err != nil {
			if !send(map[string]any{"type": "error", "code": "invalid_sync_request"}) {
				return
			}
			continue
		}
		metrics.RecordSyncMessage(ctx, "in", frame.Type, len(raw))
		if !s.handleFrame(ctx, identity, frame, send) {
			return
		}
	}
}

func (s *BrowserSyncService) handleFrame(ctx context.Context, identity SyncConnectionIdentity, frame syncClientFrame, send func(any) bool) bool {
	bounded, stop := context.WithTimeout(ctx, 10*time.Second)
	defer stop()
	switch frame.Type {
	case "pull":
		page, err := s.store.PullRecords(bounded, identity, frame.After)
		if err != nil {
			return s.sendError(identity, frame.RequestID, err, send)
		}
		return send(map[string]any{"type": "records", "request_id": frame.RequestID, "page": page})
	case "push":
		if allowed, wait := s.publishes.take(identity.SessionID, time.Now()); !allowed {
			return send(map[string]any{"type": "error", "request_id": frame.RequestID, "code": "sync_rate_limited", "retry_after_ms": wait.Milliseconds()})
		}
		results, err := s.store.PushRecords(bounded, identity, frame.Writes)
		if err != nil {
			return s.sendError(identity, frame.RequestID, err, send)
		}
		return send(map[string]any{"type": "pushed", "request_id": frame.RequestID, "results": results})
	default:
		// A frame from a newer client.
		return true
	}
}

// sendError answers a refused request. A revoked session's socket ends there.
func (s *BrowserSyncService) sendError(identity SyncConnectionIdentity, request string, err error, send func(any) bool) bool {
	code, _ := syncErrorCode(err)
	if !send(map[string]any{"type": "error", "request_id": request, "code": code}) {
		return false
	}
	return code != "sync_session_forbidden"
}

func (s *BrowserSyncService) writeLoop(ctx context.Context, conn *websocket.Conn, identity SyncConnectionIdentity, events <-chan db.AccountEvent, outgoing <-chan any) {
	write := func(value any) error {
		if err := conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
			return err
		}
		return writeSyncJSON(ctx, conn, value)
	}
	vault, err := s.store.Vault(ctx, identity.UserID)
	if err != nil || vault == nil || vault.VaultID != identity.VaultID {
		return
	}
	if write(map[string]any{"type": "welcome", "vault": vault, "session_id": identity.SessionID}) != nil {
		return
	}
	if write(s.batchPolicyFrame()) != nil {
		return
	}
	// Transport keepalive only: pings keep proxies and NAT mappings open and detect dead peers.
	// They never touch the database, so an idle socket costs nothing.
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	// The socket outlives the access token that minted its ticket, so it confirms in band that the
	// minting login is still active.
	revalidate := time.NewTimer(jittered(s.sessionRevalidation(), 0.25))
	defer revalidate.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-revalidate.C:
			if !s.revalidate(ctx, conn, identity, revalidate) {
				return
			}
		case frame := <-outgoing:
			if write(frame) != nil {
				return
			}
		case event := <-events:
			if !s.handleEvent(ctx, conn, identity, event, write) {
				return
			}
		case <-ping.C:
			if conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)) != nil {
				return
			}
			metrics.RecordSyncMessage(ctx, "out", "ping", 0)
		}
	}
}

func (s *BrowserSyncService) revalidate(ctx context.Context, conn *websocket.Conn, identity SyncConnectionIdentity, timer *time.Timer) bool {
	bounded, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	active, err := s.database.AccountSessionActive(bounded, identity.SessionHash, identity.UserID)
	if err == nil && !active {
		closeBrowserSyncForReconnect(conn)
		return false
	}
	next := jittered(s.sessionRevalidation(), 0.25)
	if err != nil {
		// A database outage must not disconnect everyone at once.
		next = jittered(min(time.Minute, s.sessionRevalidation()), 0.5)
	}
	timer.Reset(next)
	return true
}

// handleEvent turns account events into hints. Clients pull when told something changed; a lost
// database listener arrives as "reset" and gets the same answer.
func (s *BrowserSyncService) handleEvent(ctx context.Context, conn *websocket.Conn, identity SyncConnectionIdentity, event db.AccountEvent, write func(any) error) bool {
	switch event.Topic {
	case "reset", syncTopicRecords:
		if event.Topic == "reset" && write(s.batchPolicyFrame()) != nil {
			return false
		}
		return write(map[string]any{"type": "changed"}) == nil
	case syncTopicSessions:
		bounded, stop := context.WithTimeout(ctx, 5*time.Second)
		defer stop()
		active, err := s.store.SessionActive(bounded, identity)
		if err == nil && !active {
			_ = write(map[string]any{"type": "error", "code": "sync_session_forbidden"})
			return false
		}
		return write(map[string]any{"type": "sessions_changed"}) == nil
	case syncTopicVaultKey:
		return write(map[string]any{"type": "vault_key_changed"}) == nil
	case syncTopicVaultGone:
		_ = write(map[string]any{"type": "vault_gone"})
		return false
	default:
		return true
	}
}

// Encode once and count the successful payload, including Encoder's newline.
func writeSyncJSON(ctx context.Context, conn *websocket.Conn, value any) error {
	var payload bytes.Buffer
	if err := json.NewEncoder(&payload).Encode(value); err != nil {
		return err
	}
	if err := conn.WriteMessage(websocket.TextMessage, payload.Bytes()); err != nil {
		return err
	}
	frame, _ := value.(map[string]any)
	kind, _ := frame["type"].(string)
	metrics.RecordSyncMessage(ctx, "out", kind, payload.Len())
	return nil
}

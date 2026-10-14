package api

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/kannachi323/misty/server/internal/accounts"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

const (
	deviceChannelFrameLimit   = 16 << 10
	deviceChannelPing         = 30 * time.Second
	deviceChannelReadDeadline = 75 * time.Second
	deviceChannelBurstLimit   = 32
)

// A one-use ticket AND a fresh device-key signature open the socket, so no
// cookie-only browser upgrade can join it.
var deviceChannelUpgrader = websocket.Upgrader{ReadBufferSize: 4096, WriteBufferSize: 4096,
	CheckOrigin: func(*http.Request) bool { return true }, HandshakeTimeout: 10 * time.Second}

// deviceChannelFrame is what a device sends. After "authenticate" the device
// only listens; any other frame is answered with invalid_device_frame.
type deviceChannelFrame struct {
	Type      string `json:"type"`
	Signature string `json:"signature,omitempty"`
}

// DeviceChannelTicket issues a one-use, 60-second ticket for the device socket.
func (s *AgentsService) DeviceChannelTicket() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		deviceID := chi.URLParam(r, "deviceID")
		if !deviceIDPattern.MatchString(deviceID) {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		token, err := security.GenerateSecureToken()
		if err != nil {
			writeAgentError(w, err)
			return
		}
		session := ""
		if sid := accounts.SessionID(r); sid != "" {
			session = security.HashToken(sid)
		}
		if err := s.database.CreateDeviceChannelTicket(r.Context(), userID, deviceID, security.HashToken(token), session); err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"ticket": token, "expiresIn": 60})
	}
}

// DeviceChannel is the control channel: presence, admission, revocation and
// account-event hints. Devices never reach each other through it; it never
// carries file data.
func (s *AgentsService) DeviceChannel() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !websocket.IsWebSocketUpgrade(r) {
			w.Header().Set("Upgrade", "websocket")
			w.WriteHeader(http.StatusUpgradeRequired)
			return
		}
		token := r.URL.Query().Get("ticket")
		if len(token) < 32 || len(token) > 128 {
			http.Error(w, "device channel ticket required", http.StatusForbidden)
			return
		}
		identity, err := s.database.ConsumeDeviceChannelTicket(r.Context(), security.HashToken(token))
		if err != nil {
			http.Error(w, "device channel ticket required", http.StatusForbidden)
			return
		}
		publicKey, ok := decodeDevicePublicKey(identity.PublicKey)
		if !ok {
			http.Error(w, "device identity required", http.StatusForbidden)
			return
		}
		conn, err := deviceChannelUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		hub := s.devices()
		challenge, err := security.GenerateSecureToken()
		if err != nil {
			return
		}
		conn.SetReadLimit(deviceChannelFrameLimit)
		_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if conn.WriteJSON(map[string]any{"type": "challenge", "challenge": challenge, "instance": hub.instance}) != nil {
			return
		}
		var auth deviceChannelFrame
		if conn.ReadJSON(&auth) != nil || auth.Type != "authenticate" {
			return
		}
		signature, err := base64.StdEncoding.DecodeString(auth.Signature)
		if err != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(publicKey, deviceChannelProof(identity.UserID, identity.DeviceID, hub.instance, challenge), signature) {
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "Device proof required"), time.Now().Add(time.Second))
			return
		}
		s.serveDeviceChannel(r.Context(), conn, identity)
	}
}

func (s *AgentsService) serveDeviceChannel(parent context.Context, conn *websocket.Conn, identity *db.DeviceChannelIdentity) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	hub := s.devices()
	outgoing := make(chan any, 64)
	var closeOnce sync.Once
	closeConn := func() { closeOnce.Do(func() { cancel(); _ = conn.Close() }) }
	send := func(frame any) bool {
		select {
		case outgoing <- frame:
			return true
		case <-ctx.Done():
			return false
		default:
			// A reader that cannot keep up loses its socket, not the hub.
			closeConn()
			return false
		}
	}
	// A removed device is told before its socket closes.
	revoke := func() {
		select {
		case outgoing <- deviceRevokedFrame{}:
		default:
			closeConn()
		}
	}
	connection := &deviceConnection{
		id: uuid.NewString(), userID: identity.UserID, deviceID: identity.DeviceID, admitted: identity.State == "admitted",
		send: send, close: closeConn, revoke: revoke, presence: devicePresence{DeviceID: identity.DeviceID},
	}
	if !hub.register(connection) {
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "Too many devices"), time.Now().Add(time.Second))
		return
	}
	defer hub.unregister(connection)
	_ = s.database.MarkDevicesSeen(ctx, []string{identity.DeviceID})
	events, unsubscribe, err := s.database.SubscribeAccountEvents(ctx, identity.UserID)
	if err != nil {
		return
	}
	defer unsubscribe()

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer closeConn()
		ping := time.NewTicker(deviceChannelPing)
		defer ping.Stop()
		write := func(frame any) bool {
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			return conn.WriteJSON(frame) == nil
		}
		ready := map[string]any{"type": "ready", "deviceId": identity.DeviceID, "state": identity.State}
		if hub.isAdmitted(connection) {
			ready["presence"] = hub.snapshot(identity.UserID)
		}
		if !write(ready) {
			return
		}
		for {
			select {
			case <-ctx.Done():
				return
			case frame := <-outgoing:
				if _, revoked := frame.(deviceRevokedFrame); revoked {
					_ = write(map[string]any{"type": "revoked"})
					_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "Device removed"), time.Now().Add(time.Second))
					return
				}
				if !write(frame) {
					return
				}
			case event := <-events:
				switch event.Topic {
				case "devices", "device-admission", "jobs", "reset":
				default:
					continue
				}
				if event.Topic == "devices" || event.Topic == "reset" {
					// A removed device learns it here even if the kick was missed.
					state, stateErr := s.database.DeviceAdmissionState(ctx, identity.UserID, identity.DeviceID)
					if stateErr == nil && state == "revoked" {
						_ = write(map[string]any{"type": "revoked"})
						return
					}
					if stateErr == nil && state == "admitted" && hub.admit(connection) {
						if !write(map[string]any{"type": "admitted", "presence": hub.snapshot(identity.UserID)}) {
							return
						}
					}
				}
				if !write(map[string]any{"type": "event", "topic": event.Topic}) {
					return
				}
			case <-ping.C:
				_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)) != nil {
					return
				}
			}
		}
	}()
	defer func() { closeConn(); <-done }()

	_ = conn.SetReadDeadline(time.Now().Add(deviceChannelReadDeadline))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(deviceChannelReadDeadline)) })
	windowStart, messages := time.Now(), 0
	for {
		var frame deviceChannelFrame
		if err := conn.ReadJSON(&frame); err != nil {
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(deviceChannelReadDeadline))
		if time.Since(windowStart) >= 5*time.Second {
			windowStart, messages = time.Now(), 0
		}
		messages++
		if messages > deviceChannelBurstLimit {
			return
		}
		// The device only listens once authenticated.
		send(map[string]any{"type": "error", "code": "invalid_device_frame"})
	}
}

// deviceRevokedFrame asks the writer to tell the device it was removed, then close.
type deviceRevokedFrame struct{}

func (s *AgentsService) devices() *deviceHub {
	s.deviceHubOnce.Do(func() {
		s.deviceHub = newDeviceHub(s.database)
		s.deviceHub.start(context.Background())
	})
	return s.deviceHub
}

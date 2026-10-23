package api

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

func (s *RealtimeService) Connect() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Check the upgrade before consuming the single-use ticket. A reverse
		// proxy that drops the Upgrade headers must not invalidate a valid ticket.
		if !websocket.IsWebSocketUpgrade(r) {
			log.Printf("Realtime WebSocket upgrade headers missing")
			w.Header().Set("Upgrade", "websocket")
			writeJSON(w, http.StatusUpgradeRequired, map[string]string{"code": "websocket_upgrade_required"})
			return
		}
		token := r.URL.Query().Get("ticket")
		if token == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_ticket"})
			return
		}
		userID, after, err := s.database.ConsumeRealtimeTicket(r.Context(), security.HashToken(token))
		if err != nil {
			log.Printf("Realtime WebSocket ticket rejected: %v", err)
			writeSpaceError(w, err)
			return
		}
		conn, err := realtimeUpgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("Realtime WebSocket upgrade failed: %v", err)
			return
		}
		client := &RealtimeClient{UserID: userID, conn: conn, Send: make(chan []byte, 256), Done: make(chan struct{})}
		events, resync, err := s.database.SpaceEventsAfter(r.Context(), userID, after, 500)
		if err != nil {
			log.Printf("Realtime WebSocket replay failed for user %s after %d: %v", userID, after, err)
			_ = conn.Close()
			return
		}
		initial, _ := json.Marshal(map[string]any{"type": "replay", "events": events, "resync_required": resync})
		if err := conn.WriteMessage(websocket.TextMessage, initial); err != nil {
			_ = conn.Close()
			return
		}
		s.register(client)
		go s.writeLoop(client)
		s.readLoop(client)
	}
}

func (s *RealtimeService) register(client *RealtimeClient) {
	s.Mu.Lock()
	s.clients[client] = struct{}{}
	if s.clientsByUser[client.UserID] == nil {
		s.clientsByUser[client.UserID] = map[*RealtimeClient]struct{}{}
	}
	s.clientsByUser[client.UserID][client] = struct{}{}
	s.Mu.Unlock()
}

func (s *RealtimeService) Unregister(client *RealtimeClient) {
	s.Mu.Lock()
	if _, ok := s.clients[client]; ok {
		delete(s.clients, client)
		delete(s.clientsByUser[client.UserID], client)
		if len(s.clientsByUser[client.UserID]) == 0 {
			delete(s.clientsByUser, client.UserID)
		}
		client.once.Do(func() { close(client.Done) })
	}
	previousSpaceID := client.viewingSpaceID
	if previousSpaceID != "" {
		s.removeViewerLocked(previousSpaceID, client)
		client.viewingSpaceID = ""
	}
	s.Mu.Unlock()
	if previousSpaceID != "" {
		s.broadcastPresence(previousSpaceID)
	}
}

// removeViewerLocked removes client from spaceID's viewer set. Callers must
// hold s.mu.
func (s *RealtimeService) removeViewerLocked(spaceID string, client *RealtimeClient) {
	viewers := s.Viewers[spaceID]
	if viewers == nil {
		return
	}
	delete(viewers, client)
	if len(viewers) == 0 {
		delete(s.Viewers, spaceID)
	}
}

func (s *RealtimeService) readLoop(client *RealtimeClient) {
	defer func() { s.Unregister(client); _ = client.conn.Close() }()
	client.conn.SetReadLimit(4096)
	_ = client.conn.SetReadDeadline(time.Now().Add(realtimePongWait))
	client.conn.SetPongHandler(func(string) error { return client.conn.SetReadDeadline(time.Now().Add(realtimePongWait)) })
	for {
		_, payload, err := client.conn.ReadMessage()
		if err != nil {
			return
		}
		s.handleClientMessage(client, payload)
	}
}

func (s *RealtimeService) writeLoop(client *RealtimeClient) {
	ticker := time.NewTicker(realtimePingPeriod)
	defer ticker.Stop()
	for {
		select {
		case payload := <-client.Send:
			_ = client.conn.SetWriteDeadline(time.Now().Add(realtimeWriteWait))
			if err := client.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				return
			}
		case <-ticker.C:
			_ = client.conn.SetWriteDeadline(time.Now().Add(realtimeWriteWait))
			if err := client.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-s.closed:
			return
		case <-client.Done:
			return
		}
	}
}

// ConnectionCount reports how many realtime WebSockets are currently held.
//
// These connections are long-lived, so an edge proxy sees only the upgrade and
// never how many are still open. This is the only place that number exists.
func (s *RealtimeService) ConnectionCount() int {
	if s == nil {
		return 0
	}
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	return len(s.clients)
}

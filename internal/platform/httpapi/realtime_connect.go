package api

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/misty-org/misty-server/internal/platform/db"
	"github.com/misty-org/misty-server/internal/platform/security"
)

const realtimeReplayLimit = 500

func (s *RealtimeService) Connect() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
		if only := r.URL.Query().Get("only"); only == "drive." {
			client.eventPrefix = only
		}
		s.register(client)
		events, recent, resync, newest := []db.SpaceEvent{}, []db.SpaceEvent{}, false, int64(0)
		if after >= 0 && r.URL.Query().Get("live") != "1" {
			var replay db.SpaceReplay
			replay, err = s.database.SpaceEventsReplay(r.Context(), userID, after, realtimeReplayLimit)
			if replay.Events != nil {
				events = replay.Events
			}
			if replay.Recent != nil {
				recent = replay.Recent
			}
			resync, newest = replay.Resync, replay.Newest
		}
		if err != nil {
			log.Printf("Realtime WebSocket replay failed for user %s after %d: %v", userID, after, err)
			s.Unregister(client)
			_ = conn.Close()
			return
		}
		if len(events) >= realtimeReplayLimit {
			resync = true
		}
		if !resync {
			events = append(recent, events...)
		}
		replayMessage := map[string]any{"type": "replay", "events": events, "resync_required": resync}
		if resync && newest > 0 {
			replayMessage["newest_event_id"] = newest
		}
		initial, _ := json.Marshal(replayMessage)
		if err := conn.WriteMessage(websocket.TextMessage, initial); err != nil {
			s.Unregister(client)
			_ = conn.Close()
			return
		}
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
	go s.PublishStatus(client.UserID)
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
	holds := client.driveHolds
	client.driveHolds = nil
	typing := make([]string, 0, len(client.typingScopes))
	for scope := range client.typingScopes {
		typing = append(typing, scope)
	}
	agentSpaces := map[string]bool{}
	for key := range client.agentStates {
		if spaceID, _, found := strings.Cut(key, "\x00"); found && spaceID != "" {
			agentSpaces[spaceID] = true
		}
	}
	client.agentStates = nil
	s.Mu.Unlock()
	if len(typing) > 0 || len(agentSpaces) > 0 {
		go s.clearLeftovers(client, typing, agentSpaces)
	}
	if len(holds) > 0 {
		go s.releaseDriveHolds(client.UserID, holds)
	}
	if previousSpaceID != "" {
		s.broadcastPresence(previousSpaceID)
	}
	go s.leaveVoice(client)
	go s.PublishStatus(client.UserID)
}

func (s *RealtimeService) clearLeftovers(client *RealtimeClient, typing []string, agentSpaces map[string]bool) {
	for _, scope := range typing {
		spaceID, conversationID, _ := strings.Cut(scope, "\x00")
		s.Mu.Lock()
		if s.otherClientHoldsLocked(client, func(other *RealtimeClient) bool { return other.typingScopes[scope] }) {
			s.Mu.Unlock()
			continue
		}
		if client.typingScopes == nil {
			client.typingScopes = map[string]bool{}
		}
		client.typingScopes[scope] = true
		client.lastTypingAt = time.Time{}
		s.Mu.Unlock()
		s.broadcastTyping(client, spaceID, conversationID, false)
	}
	for spaceID := range agentSpaces {
		prefix := spaceID + "\x00"
		s.Mu.Lock()
		if s.otherClientHoldsLocked(client, func(other *RealtimeClient) bool {
			for key := range other.agentStates {
				if strings.HasPrefix(key, prefix) {
					return true
				}
			}
			return false
		}) {
			s.Mu.Unlock()
			continue
		}
		client.lastAgentsAt = time.Time{}
		s.Mu.Unlock()
		s.broadcastAgents(client, spaceID, nil)
	}
}

func (s *RealtimeService) otherClientHoldsLocked(client *RealtimeClient, holds func(*RealtimeClient) bool) bool {
	for other := range s.clientsByUser[client.UserID] {
		if other != client && holds(other) {
			return true
		}
	}
	return false
}

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
	client.conn.SetReadLimit(16 << 10)
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

func (s *RealtimeService) ConnectionCount() int {
	if s == nil {
		return 0
	}
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	return len(s.clients)
}

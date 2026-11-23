package api

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/misty-org/misty-server/internal/platform/db"
)

var realtimeReset = []byte(`{"type":"replay","events":[],"resync_required":true}`)

func (s *RealtimeService) deliver(client *RealtimeClient, payload []byte) {
	select {
	case <-client.Done:
		return
	default:
	}
	select {
	case client.Send <- payload:
	default:
		if client.conn != nil {
			_ = client.conn.Close()
		}
		s.Unregister(client)
	}
}

func (s *RealtimeService) broadcastReset() {
	s.Mu.RLock()
	clients := make([]*RealtimeClient, 0, len(s.clients))
	for client := range s.clients {
		clients = append(clients, client)
	}
	s.Mu.RUnlock()
	for _, client := range clients {
		s.deliver(client, realtimeReset)
	}
}

func (s *RealtimeService) BroadcastEvent(eventID int64) {
	s.Mu.RLock()
	empty := len(s.clients) == 0
	s.Mu.RUnlock()
	if empty {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	members, err := s.database.SpaceEventCandidateUsers(ctx, eventID)
	if err != nil {
		s.broadcastReset()
		return
	}
	s.Mu.RLock()
	targets := map[string][]*RealtimeClient{}
	for _, user := range members {
		for client := range s.clientsByUser[user] {
			targets[user] = append(targets[user], client)
		}
	}
	s.Mu.RUnlock()
	if len(targets) == 0 {
		return
	}
	users := make([]string, 0, len(targets))
	for user := range targets {
		users = append(users, user)
	}
	event, visible, err := s.database.SpaceEventForUsers(ctx, eventID, users)
	if errors.Is(err, db.ErrSpaceNotFound) {
		return
	}
	if err != nil {
		s.broadcastEventPerUser(ctx, eventID, targets)
		return
	}
	if len(visible) == 0 {
		return
	}
	payload, err := json.Marshal(map[string]any{"type": "event", "event": event})
	if err != nil {
		s.broadcastReset()
		return
	}
	for _, user := range visible {
		for _, client := range targets[user] {
			s.deliver(client, payload)
		}
	}
}

func (s *RealtimeService) broadcastEventPerUser(ctx context.Context, eventID int64, targets map[string][]*RealtimeClient) {
	var payload []byte
	for user, clients := range targets {
		event, err := s.database.EventByIDForUser(ctx, user, eventID)
		if errors.Is(err, db.ErrSpaceNotFound) {
			continue
		}
		if err != nil {
			for _, client := range clients {
				s.deliver(client, realtimeReset)
			}
			continue
		}
		if payload == nil {
			if payload, err = json.Marshal(map[string]any{"type": "event", "event": event}); err != nil {
				s.broadcastReset()
				return
			}
		}
		for _, client := range clients {
			s.deliver(client, payload)
		}
	}
}

func (s *RealtimeService) ViewerSpaceCount() int {
	if s == nil {
		return 0
	}
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	return len(s.Viewers)
}

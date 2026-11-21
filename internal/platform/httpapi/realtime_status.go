package api

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/misty-org/misty-server/internal/platform/db"
)

// AccountStatus is how one person shows to others: online, idle, dnd or offline, and their custom
// line. Invisible shows as offline, with no custom line.
type AccountStatus struct {
	UserID      string `json:"user_id"`
	Status      string `json:"status"`
	CustomText  string `json:"custom_text,omitempty"`
	CustomEmoji string `json:"custom_emoji,omitempty"`
}

// setIdle records whether this computer has gone idle, and tells others if the account's status
// changed with it.
func (s *RealtimeService) setIdle(client *RealtimeClient, idle bool) {
	s.Mu.Lock()
	changed := client.idle != idle
	client.idle = idle
	s.Mu.Unlock()
	if changed {
		go s.PublishStatus(client.UserID)
	}
}

// connectionStatusLocked is what the account's connections say: offline with none, idle when every
// one is idle, otherwise online. Callers hold s.Mu (read is enough).
func (s *RealtimeService) connectionStatusLocked(userID string) string {
	clients := s.clientsByUser[userID]
	if len(clients) == 0 {
		return "offline"
	}
	for client := range clients {
		if !client.idle {
			return "online"
		}
	}
	return "idle"
}

func shownStatus(userID, connection string, chosen db.ChosenStatus) AccountStatus {
	out := AccountStatus{UserID: userID, Status: connection}
	if connection == "offline" || chosen.Chosen == "invisible" {
		out.Status = "offline"
		return out
	}
	if chosen.Chosen == "dnd" {
		out.Status = "dnd"
	}
	out.CustomText, out.CustomEmoji = chosen.CustomText, chosen.CustomEmoji
	return out
}

// Statuses returns how each of these people shows right now.
func (s *RealtimeService) Statuses(ctx context.Context, userIDs []string) ([]AccountStatus, error) {
	chosen, err := s.database.ChosenStatuses(ctx, userIDs)
	if err != nil {
		return nil, err
	}
	out := make([]AccountStatus, 0, len(chosen))
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	for id, choice := range chosen {
		out = append(out, shownStatus(id, s.connectionStatusLocked(id), choice))
	}
	return out, nil
}

// PublishStatus sends the account's status to its audience (friends and people it shares a Space
// with), when it changed since it was last sent.
func (s *RealtimeService) PublishStatus(userID string) {
	// A realtime service made without a database (some tests) has no statuses to publish.
	if s.database == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	chosen, err := s.database.ChosenStatuses(ctx, []string{userID})
	if err != nil {
		log.Printf("realtime status for %s: %v", userID, err)
		return
	}
	s.Mu.Lock()
	status := shownStatus(userID, s.connectionStatusLocked(userID), chosen[userID])
	// Offline is no entry at all, so the map holds only accounts that are on.
	key := ""
	if status.Status != "offline" {
		key = status.Status + "\x00" + status.CustomText + "\x00" + status.CustomEmoji
	}
	if s.lastStatus[userID] == key {
		s.Mu.Unlock()
		return
	}
	if key == "" {
		delete(s.lastStatus, userID)
	} else {
		s.lastStatus[userID] = key
	}
	s.Mu.Unlock()
	audience, err := s.database.StatusAudience(ctx, userID)
	if err != nil {
		log.Printf("realtime status audience for %s: %v", userID, err)
		return
	}
	payload, err := json.Marshal(map[string]any{"type": "status", "status": status})
	if err != nil {
		return
	}
	// The person's own computers read their status on load; only others are told.
	s.SendToUsers(audience, payload)
}

// SendToUsers delivers one message to every connection of these accounts.
func (s *RealtimeService) SendToUsers(userIDs []string, payload []byte) {
	targets := []*RealtimeClient{}
	s.Mu.RLock()
	for _, id := range userIDs {
		for client := range s.clientsByUser[id] {
			targets = append(targets, client)
		}
	}
	s.Mu.RUnlock()
	for _, client := range targets {
		s.deliver(client, payload)
	}
}

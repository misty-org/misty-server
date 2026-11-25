package api

import (
	"context"
	"encoding/json"
	"log"
	"time"
)

func (s *RealtimeService) PublishProfile(userID string, fields map[string]any) {
	if s == nil || s.database == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	audience, err := s.database.StatusAudience(ctx, userID)
	if err != nil {
		log.Printf("realtime profile audience for %s: %v", userID, err)
		return
	}
	profile := map[string]any{"user_id": userID}
	for key, value := range fields {
		profile[key] = value
	}
	payload, err := json.Marshal(map[string]any{"type": "profile", "profile": profile})
	if err != nil {
		return
	}
	s.SendToUsers(append(audience, userID), payload)
}

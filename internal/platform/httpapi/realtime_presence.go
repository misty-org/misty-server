package api

import (
	"context"
	"encoding/json"
	"sort"
	"time"
)

// handleClientMessage processes viewing and ephemeral typing declarations
// sent by a connected client.
func (s *RealtimeService) handleClientMessage(client *RealtimeClient, payload []byte) {
	var msg struct {
		Type           string `json:"type"`
		SpaceID        string `json:"space_id"`
		ConversationID string `json:"conversation_id"`
		Active         bool   `json:"active"`
	}
	if json.Unmarshal(payload, &msg) != nil {
		return
	}
	if msg.Type == "viewing" {
		s.SetViewing(client, msg.SpaceID, msg.Active)
	} else if msg.Type == "typing" {
		s.broadcastTyping(client, msg.SpaceID, msg.ConversationID, msg.Active)
	}
}

// broadcastTyping sends transient state only to people who can read the same chat.
// It is never persisted; clients expire a stale active event after a few seconds.
func (s *RealtimeService) broadcastTyping(client *RealtimeClient, spaceID, conversationID string, active bool) {
	if spaceID == "" {
		return
	}
	scopeKey := spaceID + "\x00" + conversationID
	s.Mu.Lock()
	if client.typingScopes == nil {
		client.typingScopes = map[string]bool{}
	}
	if active && time.Since(client.lastTypingAt) < 250*time.Millisecond {
		s.Mu.Unlock()
		return
	}
	if !active && !client.typingScopes[scopeKey] {
		s.Mu.Unlock()
		return
	}
	client.lastTypingAt = time.Now()
	if active {
		client.typingScopes[scopeKey] = true
	} else {
		delete(client.typingScopes, scopeKey)
	}
	s.Mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	recipients := map[string]bool{}
	if conversationID == "" {
		members, err := s.database.SpaceMembers(ctx, client.UserID, spaceID)
		if err != nil {
			return
		}
		for _, member := range members {
			recipients[member.UserID] = true
		}
	} else {
		conversations, err := s.database.SpaceConversations(ctx, client.UserID, spaceID)
		if err != nil {
			return
		}
		for _, conversation := range conversations {
			if conversation.ID != conversationID {
				continue
			}
			for _, participant := range conversation.Participants {
				if participant.Kind == "person" && participant.UserID != "" {
					recipients[participant.UserID] = true
				}
			}
			break
		}
		if len(recipients) == 0 {
			return
		}
	}
	delete(recipients, client.UserID)
	payload, err := json.Marshal(map[string]any{
		"type": "typing", "space_id": spaceID, "conversation_id": conversationID,
		"user_id": client.UserID, "active": active,
	})
	if err != nil {
		return
	}
	s.Mu.RLock()
	targets := make([]*RealtimeClient, 0)
	for userID := range recipients {
		for target := range s.clientsByUser[userID] {
			targets = append(targets, target)
		}
	}
	s.Mu.RUnlock()
	for _, target := range targets {
		s.deliver(target, payload)
	}
}

// setViewing updates which space (if any) a client is actively viewing, and
// whether it currently has that space's chat in focus, then broadcasts the
// resulting presence change. A non-empty spaceID is verified against real
// space membership before being accepted — otherwise a client could claim to
// be viewing a space it has no access to and leak its presence to that
// space's real members.
func (s *RealtimeService) SetViewing(client *RealtimeClient, spaceID string, active bool) {
	if spaceID != "" {
		_, err := s.database.SpaceByID(context.Background(), client.UserID, spaceID)
		if err != nil {
			spaceID = ""
		}
	}

	s.Mu.Lock()
	previous := client.viewingSpaceID
	if previous == spaceID && client.active == active {
		s.Mu.Unlock()
		return
	}
	spaceChanged := previous != spaceID
	if spaceChanged && previous != "" {
		s.removeViewerLocked(previous, client)
	}
	if spaceID != "" {
		if s.Viewers[spaceID] == nil {
			s.Viewers[spaceID] = map[*RealtimeClient]struct{}{}
		}
		s.Viewers[spaceID][client] = struct{}{}
	}
	client.viewingSpaceID = spaceID
	client.active = active
	s.Mu.Unlock()

	if spaceChanged && previous != "" {
		s.broadcastPresence(previous)
	}
	if spaceID != "" {
		s.broadcastPresence(spaceID)
	}
}

// presenceViewer describes one distinct user viewing a space, and whether
// any of their connections currently has that space's chat in focus.
type PresenceViewer struct {
	UserID string `json:"user_id"`
	Active bool   `json:"active"`
}

// broadcastPresence sends the current list of distinct users viewing spaceID
// (and their active/idle status) to every client currently viewing it. A
// user with multiple connections (e.g. two tabs) counts as active if any one
// of them has the space in focus.
func (s *RealtimeService) broadcastPresence(spaceID string) {
	s.Mu.RLock()
	viewers := s.Viewers[spaceID]
	targets := make([]*RealtimeClient, 0, len(viewers))
	activeByUser := map[string]bool{}
	for client := range viewers {
		targets = append(targets, client)
		activeByUser[client.UserID] = activeByUser[client.UserID] || client.active
	}
	s.Mu.RUnlock()

	userIDs := make([]string, 0, len(activeByUser))
	for userID := range activeByUser {
		userIDs = append(userIDs, userID)
	}
	sort.Strings(userIDs)
	presenceViewers := make([]PresenceViewer, 0, len(userIDs))
	for _, userID := range userIDs {
		presenceViewers = append(presenceViewers, PresenceViewer{UserID: userID, Active: activeByUser[userID]})
	}

	payload, err := json.Marshal(map[string]any{
		"type":     "presence",
		"space_id": spaceID,
		"viewers":  presenceViewers,
	})
	if err != nil {
		return
	}
	for _, client := range targets {
		select {
		case client.Send <- payload:
		default:
		}
	}
}

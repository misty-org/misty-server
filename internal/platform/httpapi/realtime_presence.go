package api

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"time"
)

// handleClientMessage processes viewing and ephemeral typing declarations
// sent by a connected client.
func (s *RealtimeService) handleClientMessage(client *RealtimeClient, payload []byte) {
	var msg struct {
		Type           string        `json:"type"`
		SpaceID        string        `json:"space_id"`
		ConversationID string        `json:"conversation_id"`
		Active         bool          `json:"active"`
		Place          string        `json:"place"`
		ItemID         string        `json:"item_id"`
		Agents         []agentStatus `json:"agents"`
	}
	if json.Unmarshal(payload, &msg) != nil {
		return
	}
	if msg.Type == "viewing" {
		s.SetViewingPlace(client, msg.SpaceID, msg.Active, msg.Place, msg.ItemID)
	} else if msg.Type == "typing" {
		s.broadcastTyping(client, msg.SpaceID, msg.ConversationID, msg.Active)
	} else if msg.Type == "agents" {
		s.broadcastAgents(client, msg.SpaceID, msg.Agents)
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

// presencePlaces are the parts of Misty a viewer can say they are in. Anything else is dropped,
// so presence never carries free text.
var presencePlaces = map[string]bool{"": true, "studio": true, "lab": true}

// presenceItemID is the shape of the one item the Lab may name: a notebook's doc id.
var presenceItemID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// presenceRooms are the rooms of the Studio (Game mode's building) a viewer may say they are in,
// by their fixed names. Nothing else about where they are goes out.
var presenceRooms = map[string]bool{
	"desk": true, "office": true, "meeting": true, "lounge": true,
	"library": true, "arcade": true, "door": true,
}

// cleanPresencePlace keeps a known place, and an item only where that place names one: an
// id-shaped notebook in the Lab, or one of the Studio's rooms.
func cleanPresencePlace(place, itemID string) (string, string) {
	if !presencePlaces[place] {
		place = ""
	}
	switch {
	case place == "lab" && presenceItemID.MatchString(itemID):
	case place == "studio" && presenceRooms[itemID]:
	default:
		itemID = ""
	}
	return place, itemID
}

// SetViewing updates which space (if any) a client is actively viewing, with no place.
func (s *RealtimeService) SetViewing(client *RealtimeClient, spaceID string, active bool) {
	s.SetViewingPlace(client, spaceID, active, "", "")
}

// SetViewingPlace updates which space (if any) a client is actively viewing, whether it
// currently has that space in focus, and which part of Misty it is in (the Studio, or the Lab and
// which notebook), then broadcasts the resulting presence change. A non-empty spaceID is verified
// against real space membership before being accepted — otherwise a client could claim to be
// viewing a space it has no access to and leak its presence to that space's real members.
func (s *RealtimeService) SetViewingPlace(client *RealtimeClient, spaceID string, active bool, place, itemID string) {
	if spaceID != "" {
		_, err := s.database.SpaceByID(context.Background(), client.UserID, spaceID)
		if err != nil {
			spaceID = ""
		}
	}
	place, itemID = cleanPresencePlace(place, itemID)
	if spaceID == "" {
		place, itemID = "", ""
	}

	s.Mu.Lock()
	previous := client.viewingSpaceID
	if previous == spaceID && client.active == active && client.place == place && client.itemID == itemID {
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
	if client.place != place || client.itemID != itemID {
		client.placeAt = time.Now()
	}
	client.place = place
	client.itemID = itemID
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
	// Place is where in Misty the user last went ("studio", "lab"), across their connections.
	Place string `json:"place,omitempty"`
	// ItemID is the Lab notebook they have open, when Place is "lab".
	ItemID string `json:"item_id,omitempty"`
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
	placeByUser := map[string]*RealtimeClient{}
	for client := range viewers {
		targets = append(targets, client)
		activeByUser[client.UserID] = activeByUser[client.UserID] || client.active
		if latest := placeByUser[client.UserID]; latest == nil || client.placeAt.After(latest.placeAt) {
			placeByUser[client.UserID] = client
		}
	}
	places := map[string][2]string{}
	for userID, client := range placeByUser {
		places[userID] = [2]string{client.place, client.itemID}
	}
	s.Mu.RUnlock()

	userIDs := make([]string, 0, len(activeByUser))
	for userID := range activeByUser {
		userIDs = append(userIDs, userID)
	}
	sort.Strings(userIDs)
	presenceViewers := make([]PresenceViewer, 0, len(userIDs))
	for _, userID := range userIDs {
		place := places[userID]
		presenceViewers = append(presenceViewers, PresenceViewer{
			UserID: userID,
			Active: activeByUser[userID],
			Place:  place[0],
			ItemID: place[1],
		})
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

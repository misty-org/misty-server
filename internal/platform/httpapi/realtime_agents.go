package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
)

// agentStatus is what a member's computer says one of their agents is doing now. The agents run on
// that computer; the server only passes this on to the Space's other members. State and Doing are
// fixed codes and Files are file names, so nothing a model wrote can travel here.
type agentStatus struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	State string   `json:"state"`
	Doing string   `json:"doing"`
	Files []string `json:"files,omitempty"`
}

var agentStates = map[string]bool{"working": true, "needs-you": true, "resting": true}

var agentDoing = map[string]bool{
	"": true, "thinking": true, "browser": true, "web": true, "shell": true, "files": true,
	"space": true, "computer": true, "app": true, "agents": true, "memory": true, "working": true,
}

const (
	maxAgentStatuses = 12
	maxAgentFiles    = 5
)

// cleanAgentStatuses keeps only well-formed statuses. Anything else is dropped whole, so a client
// can't use the channel to carry free text.
func cleanAgentStatuses(raw []agentStatus) []agentStatus {
	clean := make([]agentStatus, 0, len(raw))
	for _, status := range raw {
		if len(clean) == maxAgentStatuses {
			break
		}
		name := strings.TrimSpace(status.Name)
		if status.ID == "" || len(status.ID) > 120 || name == "" || utf8.RuneCountInString(name) > 80 ||
			!agentStates[status.State] || !agentDoing[status.Doing] {
			continue
		}
		files := make([]string, 0, maxAgentFiles)
		for _, file := range status.Files {
			if len(files) == maxAgentFiles {
				break
			}
			// A name only, never a path or a line of text.
			if file == "" || len(file) > 120 || strings.ContainsAny(file, "/\\\n\r\t") {
				continue
			}
			files = append(files, file)
		}
		clean = append(clean, agentStatus{ID: status.ID, Name: name, State: status.State, Doing: status.Doing, Files: files})
	}
	return clean
}

// broadcastAgents passes a member's agent statuses to the Space's other members, and writes a line
// in the Space's activity log whenever an agent's state or kind of work changes.
func (s *RealtimeService) broadcastAgents(client *RealtimeClient, spaceID string, raw []agentStatus) {
	if spaceID == "" {
		return
	}
	statuses := cleanAgentStatuses(raw)
	s.Mu.Lock()
	if time.Since(client.lastAgentsAt) < 500*time.Millisecond {
		s.Mu.Unlock()
		return
	}
	client.lastAgentsAt = time.Now()
	if client.agentStates == nil {
		client.agentStates = map[string]string{}
	}
	changed := make([]agentStatus, 0)
	for _, status := range statuses {
		key := spaceID + "\x00" + status.ID
		now := status.State + "\x00" + status.Doing
		if client.agentStates[key] != now {
			// An agent first seen at rest has done nothing worth a line.
			if _, known := client.agentStates[key]; known || status.State != "resting" {
				changed = append(changed, status)
			}
			client.agentStates[key] = now
		}
	}
	s.Mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// Membership is checked here; someone who isn't in the Space reaches nobody.
	members, err := s.database.SpaceMembers(ctx, client.UserID, spaceID)
	if err != nil {
		return
	}
	for _, status := range changed {
		_ = s.database.RecordAgentActivity(ctx, client.UserID, spaceID, status.ID, status.Name, status.State, status.Doing)
	}
	payload, err := json.Marshal(map[string]any{
		"type": "agents", "space_id": spaceID, "user_id": client.UserID, "agents": statuses,
	})
	if err != nil {
		return
	}
	s.Mu.RLock()
	targets := make([]*RealtimeClient, 0)
	for _, member := range members {
		if member.UserID == client.UserID {
			continue
		}
		for target := range s.clientsByUser[member.UserID] {
			targets = append(targets, target)
		}
	}
	s.Mu.RUnlock()
	for _, target := range targets {
		s.deliver(target, payload)
	}
}

// AgentActivity lists a Space's recent activity log for one of its members.
func (s *SpacesService) AgentActivity() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		lines, err := s.database.ListAgentActivity(r.Context(), userID, chi.URLParam(r, "spaceID"), 200)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"activity": lines})
	}
}

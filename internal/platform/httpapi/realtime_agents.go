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

type agentStatus struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	State   string   `json:"state"`
	Doing   string   `json:"doing"`
	Files   []string `json:"files,omitempty"`
	Waiting int      `json:"waiting,omitempty"`
}

var agentStates = map[string]bool{"working": true, "needs-you": true, "landing": true, "resting": true}

var agentDoing = map[string]bool{
	"": true, "thinking": true, "browser": true, "web": true, "shell": true, "files": true,
	"space": true, "computer": true, "app": true, "agents": true, "memory": true, "working": true,
}

const (
	maxAgentStatuses = 12
	maxAgentFiles    = 5
	maxAgentWaiting  = 99
)

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
			if !claimPath(file) {
				continue
			}
			files = append(files, file)
		}
		waiting := min(max(status.Waiting, 0), maxAgentWaiting)
		clean = append(clean, agentStatus{ID: status.ID, Name: name, State: status.State, Doing: status.Doing, Files: files, Waiting: waiting})
	}
	return clean
}

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
			if _, known := client.agentStates[key]; known || status.State != "resting" {
				changed = append(changed, status)
			}
			client.agentStates[key] = now
		}
	}
	s.Mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
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

func claimPath(file string) bool {
	if file == "" || len(file) > 200 || strings.HasPrefix(file, "/") ||
		strings.ContainsAny(file, "\\\n\r\t:") {
		return false
	}
	for _, part := range strings.Split(file, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

package api

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
)

func (hub *aiInvocationHub) hasArtifact(userID, id string) bool {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	artifact := hub.artifacts[strings.TrimSpace(id)]
	return artifact != nil && artifact.OwnerUserID == userID
}

func (hub *aiInvocationHub) artifactForUser(userID, id string) *aiArtifact {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	artifact := hub.artifacts[strings.TrimSpace(id)]
	if artifact == nil || artifact.OwnerUserID != userID {
		return nil
	}
	copy := *artifact
	return &copy
}

func (hub *aiInvocationHub) reviseTaskSetArtifact(userID, id string, operations json.RawMessage) error {
	var value struct {
		Tasks []aiTaskDraft `json:"tasks"`
	}
	if json.Unmarshal(operations, &value) != nil || len(value.Tasks) < 1 || len(value.Tasks) > 20 {
		return errors.New("invalid task set")
	}
	seen := map[string]bool{}
	for index := range value.Tasks {
		value.Tasks[index].Title = strings.Join(strings.Fields(value.Tasks[index].Title), " ")
		value.Tasks[index].Notes = strings.TrimSpace(value.Tasks[index].Notes)
		if !strings.HasPrefix(value.Tasks[index].ID, "task_") || value.Tasks[index].Title == "" || len([]rune(value.Tasks[index].Title)) > 240 || len([]rune(value.Tasks[index].Notes)) > 20_000 || seen[value.Tasks[index].ID] {
			return errors.New("invalid task draft")
		}
		if value.Tasks[index].Priority != "high" && value.Tasks[index].Priority != "medium" && value.Tasks[index].Priority != "low" {
			return errors.New("invalid task priority")
		}
		seen[value.Tasks[index].ID] = true
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	artifact := hub.artifacts[strings.TrimSpace(id)]
	if artifact == nil || artifact.OwnerUserID != userID || artifact.Kind != "task_set" || artifact.State != "proposed" {
		return errors.New("artifact is not editable")
	}
	artifact.Operations = map[string]any{"tasks": value.Tasks}
	return nil
}

func (hub *aiInvocationHub) restoreArtifact(userID string, payload json.RawMessage) *aiArtifact {
	var artifact aiArtifact
	if json.Unmarshal(payload, &artifact) != nil || artifact.ID == "" {
		return nil
	}
	artifact.OwnerUserID = userID
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if existing := hub.artifacts[artifact.ID]; existing != nil {
		return existing
	}
	hub.artifacts[artifact.ID] = &artifact
	return &artifact
}

func (hub *aiInvocationHub) decideArtifact(userID, id, decision string) (*aiArtifact, bool, bool) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	artifact := hub.artifacts[strings.TrimSpace(id)]
	if artifact == nil || artifact.OwnerUserID != userID || time.Now().UTC().Format(time.RFC3339Nano) > artifact.ExpiresAt {
		return nil, false, false
	}
	if artifact.State != "proposed" {
		copy := *artifact
		return &copy, true, true
	}
	if decision == "reject" || decision == "refine" {
		artifact.State = "rejected"
	} else if decision == "accept" {
		artifact.State = "applying"
	}
	copy := *artifact
	return &copy, true, false
}

func (hub *aiInvocationHub) completeArtifact(userID, id, state, message string) (*aiArtifact, bool) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	artifact := hub.artifacts[strings.TrimSpace(id)]
	if artifact == nil || artifact.OwnerUserID != userID {
		return nil, false
	}
	artifact.State, artifact.Error = state, strings.TrimSpace(message)
	copy := *artifact
	return &copy, true
}

func (hub *aiInvocationHub) pruneLocked() {
	now := time.Now()
	for id, record := range hub.invocations {
		if now.After(record.ExpiresAt) {
			delete(hub.invocations, id)
		}
	}
	for id, artifact := range hub.artifacts {
		expires, _ := time.Parse(time.RFC3339Nano, artifact.ExpiresAt)
		if !expires.IsZero() && now.After(expires) {
			delete(hub.artifacts, id)
		}
	}
}

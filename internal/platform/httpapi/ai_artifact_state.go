package api

import (
	"encoding/json"
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
	if now.Before(hub.nextPrune) {
		return
	}
	hub.nextPrune = now.Add(time.Minute)
	for id, record := range hub.invocations {
		if now.After(record.ExpiresAt) {
			delete(hub.invocations, id)
		}
	}
	for key, id := range hub.idempotency {
		if hub.invocations[id] == nil {
			delete(hub.idempotency, key)
		}
	}
	for id, artifact := range hub.artifacts {
		expires, _ := time.Parse(time.RFC3339Nano, artifact.ExpiresAt)
		if !expires.IsZero() && now.After(expires) {
			delete(hub.artifacts, id)
		}
	}
}

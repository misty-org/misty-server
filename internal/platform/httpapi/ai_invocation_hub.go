package api

import (
	"context"
	"encoding/json"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func (hub *aiInvocationHub) create(userID, conversationID, idempotencyKey string) (*aiInvocationRecord, bool) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	hub.pruneLocked()
	key := userID + ":" + idempotencyKey
	if id := hub.idempotency[key]; id != "" {
		if existing := hub.invocations[id]; existing != nil {
			return existing, true
		}
	}
	now := time.Now().UTC()
	record := &aiInvocationRecord{ID: "invocation_" + uuid.NewString(), OwnerUserID: userID, ConversationID: conversationID, State: "queued", CreatedAt: now, ExpiresAt: now.Add(aiInvocationTTL), Notify: make(chan struct{})}
	hub.invocations[record.ID] = record
	hub.idempotency[key] = record.ID
	return record, false
}

func (hub *aiInvocationHub) setCancel(id string, cancel context.CancelFunc) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if record := hub.invocations[id]; record != nil {
		record.Cancel = cancel
	}
}

func (hub *aiInvocationHub) append(id string, event aiInvocationEvent) {
	hub.mu.Lock()
	record := hub.invocations[id]
	if record == nil || aiInvocationTerminal(record.State) {
		hub.mu.Unlock()
		return
	}
	event.ID = strconv.Itoa(len(record.Events) + 1)
	record.Events = append(record.Events, event)
	if event.State != "" {
		record.State = event.State
	} else if event.Type == "invocation.started" {
		record.State = "running"
	}
	close(record.Notify)
	record.Notify = make(chan struct{})
	userID := record.OwnerUserID
	state := record.State
	sequence := len(record.Events)
	database := hub.database
	hub.mu.Unlock()
	if database != nil {
		payload, err := json.Marshal(event)
		if err == nil {
			err = database.AppendAIInvocationEvent(context.Background(), userID, id, int64(sequence), event.Type, payload, state)
		}
		if err != nil {
			log.Printf("persist AI invocation event %s/%d: %v", id, sequence, err)
		}
	}
}

func (hub *aiInvocationHub) restore(stored db.AIInvocationRecord, events []aiInvocationEvent) *aiInvocationRecord {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if existing := hub.invocations[stored.ID]; existing != nil {
		return existing
	}
	record := &aiInvocationRecord{
		ID: stored.ID, OwnerUserID: stored.UserID, ConversationID: stored.ConversationID,
		State: stored.State, Events: append([]aiInvocationEvent(nil), events...),
		CreatedAt: stored.CreatedAt, ExpiresAt: stored.ExpiresAt, Notify: make(chan struct{}),
	}
	hub.invocations[record.ID] = record
	hub.idempotency[stored.UserID+":"+stored.IdempotencyKey] = record.ID
	return record
}

func (hub *aiInvocationHub) complete(id string) {
	hub.append(id, aiInvocationEvent{Type: "invocation.completed", State: "completed"})
}
func (hub *aiInvocationHub) fail(id, message string) {
	hub.append(id, aiInvocationEvent{Type: "invocation.failed", State: "failed", Error: message})
}
func (hub *aiInvocationHub) cancel(id string) {
	hub.append(id, aiInvocationEvent{Type: "invocation.canceled", State: "canceled"})
}

func (hub *aiInvocationHub) events(userID, id string, cursor int) ([]aiInvocationEvent, string, <-chan struct{}, bool) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	record := hub.invocations[id]
	if record == nil || record.OwnerUserID != userID || time.Now().After(record.ExpiresAt) {
		return nil, "", nil, false
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(record.Events) {
		cursor = len(record.Events)
	}
	events := append([]aiInvocationEvent(nil), record.Events[cursor:]...)
	return events, record.State, record.Notify, true
}

func (hub *aiInvocationHub) cancelForUser(userID, id string) (string, bool) {
	hub.mu.Lock()
	record := hub.invocations[strings.TrimSpace(id)]
	if record == nil || record.OwnerUserID != userID {
		hub.mu.Unlock()
		return "", false
	}
	if aiInvocationTerminal(record.State) {
		state := record.State
		hub.mu.Unlock()
		return state, true
	}
	cancel := record.Cancel
	hub.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	hub.cancel(record.ID)
	return "canceled", true
}

func (hub *aiInvocationHub) cancelAllForUser(userID string) {
	hub.mu.Lock()
	ids := []string{}
	for id, record := range hub.invocations {
		if record.OwnerUserID == userID && !aiInvocationTerminal(record.State) {
			ids = append(ids, id)
		}
	}
	hub.mu.Unlock()
	for _, id := range ids {
		hub.cancelForUser(userID, id)
	}
}

func (hub *aiInvocationHub) addTextPatchArtifact(userID, invocationID, replacement string, resolved []aiResolvedContext, body aiInvocationInput) *aiArtifact {
	hub.mu.Lock()
	sources := make([]aiCitation, 0, len(resolved))
	for _, item := range resolved {
		sources = append(sources, item.Citation)
	}
	artifact := &aiArtifact{
		ID: "artifact_" + uuid.NewString(), SchemaVersion: 1, Kind: "text_patch", Title: "Review revision", Summary: "Replace the selected text with Misty's draft.", Sources: sources,
		Operations: map[string]any{"replacement": replacement, "selection": body.Selection}, Risk: "draft", ApprovalPolicy: "visible_apply", IdempotencyKey: "artifact:" + invocationID, ExpiresAt: time.Now().UTC().Add(aiInvocationTTL).Format(time.RFC3339Nano), State: "proposed", OwnerUserID: userID,
	}
	if body.Selection != nil {
		artifact.Target = body.Selection.Object
		artifact.BaseRevision = body.Selection.Object["revision"]
	}
	hub.artifacts[artifact.ID] = artifact
	copy := *artifact
	database := hub.database
	hub.mu.Unlock()
	if database != nil {
		payload, err := json.Marshal(artifact)
		if err == nil {
			err = database.UpsertAIArtifact(context.Background(), userID, invocationID, payload)
		}
		if err != nil {
			log.Printf("persist AI artifact %s: %v", artifact.ID, err)
		}
	}
	return &copy
}

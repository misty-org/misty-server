package api

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"time"

	agent "github.com/kannachi323/misty/server/internal/agents"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// TestingValidateAISelection exercises the public invocation boundary without
// exposing the request structs outside this package.
func TestingValidateAISelection(content, hash string) error {
	body := aiInvocationInput{
		Mode: "drawer", SurfaceID: "notes", Trigger: "selection", Prompt: "Improve",
		IdempotencyKey: "testing", Selection: &aiSelectionSnapshot{
			Content: content, ContentHash: hash, Object: map[string]any{"kind": "note", "id": "note_testing"},
		},
	}
	return validateAIInvocationInput(&body)
}

func TestingValidateAIInvocationMode(mode string) error {
	body := aiInvocationInput{
		Mode: mode, SurfaceID: "notes", Trigger: "message", Prompt: "Summarize",
		IdempotencyKey: "testing",
	}
	return validateAIInvocationInput(&body)
}

func TestingValidateAIInvocationTimezone(timezone string) (string, error) {
	body := aiInvocationInput{
		Mode: "drawer", SurfaceID: "global", Trigger: "message", Prompt: "What is due today?",
		IdempotencyKey: "testing", Timezone: timezone,
	}
	err := validateAIInvocationInput(&body)
	return body.Timezone, err
}

func TestingCompileAIScheduledPrompt() string {
	return compileAIInvocationPrompt(aiInvocationInput{
		Mode: "drawer", SurfaceID: "activity", Trigger: "schedule", Prompt: "Brief me",
		IdempotencyKey: "testing", Timezone: "America/Los_Angeles",
	}, nil)
}

func TestingPublicAIInvocationErrorForHostedReset(resetAt time.Time) string {
	return publicAIInvocationError(agent.HostedAILimitReachedError{ResetAt: resetAt})
}

func TestingAgentRuntimeModelUsage(raw json.RawMessage) agent.ModelUsage {
	return agentRuntimeModelUsage(raw)
}

func TestingPublicAgentRuntimeFailure(code, message string) string {
	return publicAgentRuntimeFailure(code, message)
}

func TestingValidateAIDeviceContext(id, opaqueScope string, metadata map[string]any) error {
	body := aiInvocationInput{
		Mode: "drawer", SurfaceID: "files", Trigger: "object", Prompt: "Explain",
		IdempotencyKey: "testing", Context: []aiContextReference{{
			Kind: "files.scope", ID: id, Title: "Files", Privacy: "device",
			OpaqueScopeID: opaqueScope, Metadata: metadata,
		}},
	}
	return validateAIInvocationInput(&body)
}

func TestingAIInvocationJournalIsolation() bool {
	hub := newAIInvocationHub()
	first, existing := hub.create("user-a", "conversation-a", "request-a")
	if existing {
		return false
	}
	second, existing := hub.create("user-a", "conversation-b", "request-a")
	if !existing || second.ID != first.ID {
		return false
	}
	_, _, _, visible := hub.events("user-b", first.ID, 0)
	return !visible
}

func TestingAISearchScore(query, content string) int      { return aiSearchScore(query, content) }
func TestingAIRelevantChunk(content, query string) string { return aiRelevantChunk(content, query) }

func TestingReadAgentVoiceJSON(value string) ([]byte, string, int64, string) {
	request := httptest.NewRequest("POST", "/agent-voice/transcriptions", bytes.NewBufferString(value))
	request.Header.Set("Content-Type", "application/json")
	return readAgentVoiceRecording(httptest.NewRecorder(), request)
}

func TestingShouldRetrieveAccountContext(prompt string) bool {
	return shouldRetrieveAccountContext(prompt)
}

func TestingBoundedAIConversationHistory(prompts, replies []string) string {
	turns := make([]db.AIConversationTurnRecord, len(prompts))
	for index, prompt := range prompts {
		turns[index].InvocationID = "invocation_" + string(rune('a'+index))
		turns[index].Prompt = prompt
		if index < len(replies) {
			turns[index].Reply = replies[index]
		}
	}
	return boundedAIConversationHistory(turns, "")
}

func TestingMistyCitationIDs(answer string, ids []string) []string {
	resolved := make([]aiResolvedContext, len(ids))
	for index, id := range ids {
		resolved[index].Citation = aiCitation{ID: id, Kind: "note", Title: id}
	}
	items := mistyAnswerCitations(answer, resolved)
	result := make([]string, len(items))
	for index, item := range items {
		result[index] = item.ID
	}
	return result
}

func TestingParseAITaskDraftSummaries(value string) ([]string, error) {
	tasks, err := parseAITaskDrafts(value)
	if err != nil {
		return nil, err
	}
	result := make([]string, len(tasks))
	for index, task := range tasks {
		result[index] = task.ID + ":" + task.Priority
	}
	return result, nil
}

func TestingAIArtifactPolicy(kind string) (string, string) {
	spec := aiArtifactSpecs[kind]
	return spec.Risk, spec.ApprovalPolicy
}

func TestingParseAIStructuredArtifact(value string) (string, map[string]any, error) {
	return parseAIStructuredArtifact(value)
}

func TestingAIAgentArtifactText(artifacts, result, requestedID string) (string, string, string) {
	return aiAgentArtifactText(&db.SpaceRun{
		ID: "run_testing", Artifacts: json.RawMessage(artifacts), Result: json.RawMessage(result),
	}, requestedID)
}

package api

import (
	"encoding/json"

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

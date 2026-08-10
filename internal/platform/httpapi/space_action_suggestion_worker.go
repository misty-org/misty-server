package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type actionSuggestionModelResponse struct {
	Actions []db.SpaceActionSuggestionProposal `json:"actions"`
}

var proposalSignals = []string{"we should ", "let's ", "lets ", "how about ", "can you ", "i'll ", "i will ", "we'll ", "we will "}
var acceptanceSignals = []string{"agreed", "sounds good", "works for me", "let's do it", "lets do it", "yes,", "yes ", "okay", "ok ", "confirmed", "deal", "i'll do", "i will do"}
var cancellationSignals = []string{"cancel that", "never mind", "nevermind", "not anymore", "let's not", "lets not", "called off", "scratch that"}

// TestingActionSuggestionAgreementGate is intentionally deterministic and
// high-recall. The model remains responsible for returning no action when the
// language is uncertain, canceled, or merely exploratory.
func TestingActionSuggestionAgreementGate(messages []db.SpaceActionSuggestionContextMessage) bool {
	if len(messages) < 2 {
		return false
	}
	proposers := map[string]bool{}
	acceptors := map[string]bool{}
	for _, message := range messages {
		text := strings.ToLower(strings.TrimSpace(renderMessageText(message.Content)))
		for _, signal := range proposalSignals {
			if strings.Contains(text, signal) {
				proposers[message.UserID] = true
				break
			}
		}
		for _, signal := range acceptanceSignals {
			if strings.Contains(text, signal) {
				acceptors[message.UserID] = true
				break
			}
		}
	}
	for proposer := range proposers {
		for acceptor := range acceptors {
			if proposer != acceptor {
				return true
			}
		}
	}
	return false
}

func suggestionTranscript(messages []db.SpaceActionSuggestionContextMessage) (string, []db.SpaceActionSuggestionEvidence) {
	var b strings.Builder
	evidence := make([]db.SpaceActionSuggestionEvidence, 0, len(messages))
	remaining := 12000
	for _, message := range messages {
		text := strings.TrimSpace(renderMessageText(message.Content))
		if text == "" {
			continue
		}
		line := message.UserName + ": " + text + "\n"
		if utf8.RuneCountInString(line) > remaining {
			r := []rune(line)
			line = string(r[:remaining])
		}
		if remaining <= 0 {
			break
		}
		b.WriteString(line)
		remaining -= utf8.RuneCountInString(line)
		evidence = append(evidence, db.SpaceActionSuggestionEvidence{MessageID: message.ID, Hash: message.Hash})
	}
	return b.String(), evidence
}

func normalizeSuggestionModelResponse(raw string) ([]db.SpaceActionSuggestionProposal, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)
	var response actionSuggestionModelResponse
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		return nil, err
	}
	if len(response.Actions) < 1 || len(response.Actions) > 3 {
		return nil, db.ErrSpaceInvalid
	}
	capabilities := map[string]string{
		db.SuggestionTaskCreate: "tasks.create", db.SuggestionCalendarCreate: "calendar.events.create", db.SuggestionJournalCreate: "journal.notes.create", db.SuggestionRoadmapCreate: "roadmaps.items.create", db.SuggestionFollowUpSchedule: "conversation.follow_up.schedule",
	}
	for i := range response.Actions {
		action := &response.Actions[i]
		capability, ok := capabilities[action.ActionKind]
		if !ok || strings.TrimSpace(action.Title) == "" {
			return nil, db.ErrSpaceInvalid
		}
		action.RequiredCapability = capability
		if len(action.ProposedInput) == 0 {
			action.ProposedInput = json.RawMessage(`{}`)
		}
		var value map[string]any
		if json.Unmarshal(action.ProposedInput, &value) != nil {
			return nil, db.ErrSpaceInvalid
		}
		defaults := map[string]map[string]any{
			db.SuggestionTaskCreate:       {"title": action.Title, "notes": action.Summary, "priority": "medium", "due_at": nil, "due_timezone": "UTC", "assignee_user_id": ""},
			db.SuggestionCalendarCreate:   {"title": action.Title, "description": action.Summary, "location": "", "starts_at": "", "ends_at": "", "all_day": false, "timezone": "UTC", "calendar_source_id": "misty"},
			db.SuggestionJournalCreate:    {"title": action.Title, "markdown": action.Summary},
			db.SuggestionRoadmapCreate:    {"title": action.Title, "description": action.Summary, "roadmap_id": ""},
			db.SuggestionFollowUpSchedule: {"reminder_text": action.Title, "deliver_at": "", "timezone": "UTC", "recipient_user_ids": []string{}},
		}[action.ActionKind]
		for key, fallback := range defaults {
			if _, exists := value[key]; !exists {
				value[key] = fallback
			}
		}
		action.ProposedInput, _ = json.Marshal(value)
	}
	return response.Actions, nil
}

func TestingNormalizeSuggestionModelResponse(raw string) ([]db.SpaceActionSuggestionProposal, error) {
	return normalizeSuggestionModelResponse(raw)
}

func (s *SpacesService) ProcessActionSuggestionJobs(ctx context.Context, limit int) (int, error) {
	if err := s.database.ExpireSpaceActionSuggestions(ctx); err != nil {
		return 0, err
	}
	if strings.EqualFold(strings.TrimSpace(envconfig.Getenv("MISTY_ACTION_SUGGESTIONS_KILL_SWITCH")), "true") {
		return 0, nil
	}
	jobs, err := s.database.LeaseSpaceActionSuggestionJobs(ctx, limit)
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, job := range jobs {
		owner, messages, allowed, contextErr := s.database.SpaceActionSuggestionContext(ctx, job)
		if contextErr != nil {
			_ = s.database.FinishSpaceActionSuggestionJob(ctx, job.ID, "failed", "context_failed")
			continue
		}
		if !allowed || !TestingActionSuggestionAgreementGate(messages) {
			_ = s.database.FinishSpaceActionSuggestionJob(ctx, job.ID, "skipped", "no_explicit_agreement")
			processed++
			continue
		}
		if s.agent == nil {
			_ = s.database.FinishSpaceActionSuggestionJob(ctx, job.ID, "failed", "model_unavailable")
			continue
		}
		transcript, evidence := suggestionTranscript(messages)
		prompt := `You are Misty's neutral action-opportunity detector, not an agent or participant. Analyze only this supplied conversation excerpt. Return strict JSON and no prose: {"actions":[{"action_kind":"task.create|calendar.event.create|journal.note.create|roadmap.item.create|conversation.follow_up.schedule","title":"...","summary":"...","proposed_input":{...}}]}. Use these payload fields: task {title,notes,priority,due_at,due_timezone,assignee_user_id}; calendar {title,description,location,starts_at,ends_at,all_day,timezone,calendar_source_id}; note {title,markdown}; roadmap {title,description,roadmap_id}; follow-up {reminder_text,deliver_at,timezone,recipient_user_ids}. Return {"actions":[]} unless at least two distinct people show a concrete proposal plus explicit acceptance, commitment, assignment, or scheduling. Never infer agreement from uncertainty, questions, jokes, brainstorming, or silence. If later text cancels the agreement, return no actions. Do not include hidden reasoning. Use at most three non-duplicative actions. Conversation:\n` + transcript
		if err := s.database.ConsumeSpaceActionSuggestionAllowance(ctx, job.SpaceID); err != nil {
			_ = s.database.FinishSpaceActionSuggestionJob(ctx, job.ID, "skipped", "allowance_exhausted")
			continue
		}
		text, _, modelErr := s.agent.CompleteWithModelContext(ctx, owner, prompt, db.CreditMeterAutomationAI, serveragent.InitialSelectedModelID)
		if modelErr != nil {
			_ = s.database.FinishSpaceActionSuggestionJob(ctx, job.ID, "failed", "model_failed")
			continue
		}
		proposals, parseErr := normalizeSuggestionModelResponse(text)
		if parseErr != nil || len(proposals) == 0 {
			if latest := messages[len(messages)-1:]; len(latest) > 0 {
				value := strings.ToLower(renderMessageText(latest[0].Content))
				for _, signal := range cancellationSignals {
					if strings.Contains(value, signal) {
						_ = s.database.InvalidateActiveSpaceActionSuggestions(ctx, job.SpaceID, job.Scope.ConversationID)
						break
					}
				}
			}
			_ = s.database.FinishSpaceActionSuggestionJob(ctx, job.ID, "skipped", "no_action")
			processed++
			continue
		}
		if _, err := s.database.CompleteSpaceActionSuggestionJob(ctx, job, evidence, proposals); err != nil && err != db.ErrSpaceConflict {
			_ = s.database.FinishSpaceActionSuggestionJob(ctx, job.ID, "failed", "persist_failed")
			continue
		}
		processed++
	}
	return processed, nil
}

func testingSuggestionPrompt(messages []db.SpaceActionSuggestionContextMessage) string {
	transcript, _ := suggestionTranscript(messages)
	return fmt.Sprintf("%s", transcript)
}

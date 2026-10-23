package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/platform/db"
)

func (s *AIService) mistyConversationFromSummary(r *http.Request, userID string, summary db.AgentSessionSummary) (mistyConversation, error) {
	turns, err := s.database.AIConversationTurns(r.Context(), userID, summary.ID)
	if err != nil {
		return mistyConversation{}, err
	}
	// Empty canonical conversations have no legacy Go transcript. They must be
	// visible before admission so another renderer can attach the first task.
	if len(turns) > 0 || !summary.HasLegacySession {
		notes, notesErr := s.database.AIConversationSummary(r.Context(), userID, summary.ID)
		if notesErr != nil {
			return mistyConversation{}, notesErr
		}
		messages := make([]mistyConversationMessage, 0, len(turns)*2)
		for _, turn := range turns {
			mode := "ask"
			if turn.AgentRunID != "" {
				mode = "action"
			}
			attachments := []mistyConversationAttachment{}
			if stored, attachmentErr := s.database.AIConversationAttachmentsForInvocation(r.Context(), userID, turn.InvocationID); attachmentErr == nil {
				for _, item := range stored {
					attachments = append(attachments, mistyConversationAttachment{
						ID: item.ID, Name: item.DisplayName, MIMEType: item.MIMEType, ByteSize: item.ByteSize,
						Width: item.Width, Height: item.Height,
						PreviewURL: "/misty/attachments/" + item.ID + "/content?variant=model", State: "ready",
					})
				}
			}
			if prompt := publicMistyConversationContent(turn.Prompt); prompt != "" || len(attachments) > 0 {
				messages = append(messages, mistyConversationMessage{
					ID: turn.InvocationID + "-user", Role: "user", Mode: mode,
					Content: prompt, CreatedAt: turn.CreatedAt.UTC().Format(time.RFC3339Nano), State: "completed", Attachments: attachments,
					Source: mistyTurnSource(turn.Trigger),
				})
			}
			events, pendingArtifact, reviewErr := s.database.AIConversationReview(r.Context(), userID, turn.InvocationID)
			if reviewErr != nil {
				return mistyConversation{}, reviewErr
			}
			{
				for _, event := range events {
					if event.EventType != "user.steering" {
						continue
					}
					var input struct {
						Text string `json:"text"`
					}
					if json.Unmarshal(event.Payload, &input) == nil {
						messages = append(messages, mistyConversationMessage{ID: fmt.Sprintf("%s-steering-%d", turn.InvocationID, event.Sequence), Role: "user", Mode: mode, Content: input.Text, State: "completed", CreatedAt: event.CreatedAt.UTC().Format(time.RFC3339Nano)})
					}
				}
			}
			response := strings.TrimSpace(turn.Reply)
			if response == "" {
				response = strings.TrimSpace(turn.Failure)
			}
			if response == "" {
				response = strings.TrimSpace(turn.AgentError)
			}
			if response == "" && turn.AgentRunID != "" {
				response = strings.TrimSpace(turn.Status)
				if response == "" {
					response = "Misty is working on this task."
				}
			}
			if response == "" && turn.State == "canceled" {
				response = "This task was canceled."
			}
			if response != "" || turn.State == "queued" || turn.State == "running" || strings.HasPrefix(turn.State, "awaiting_") {
				var action *mistyConversationAction
				if turn.AgentRunID != "" {
					resultHref := "/agents?conversation=" + summary.ID
					if turn.ResultSpaceID != "" && turn.ResultDrawingID != "" {
						resultHref = "/spaces/" + turn.ResultSpaceID + "/drawings/" + turn.ResultDrawingID
					}
					action = &mistyConversationAction{
						ID: turn.InvocationID + "-action", Title: "Misty task",
						Summary: response, Prompt: turn.Prompt, Risk: "write",
						State:      mistyRunActionState(turn.AgentState),
						RunID:      turn.AgentRunID,
						ResultHref: resultHref,
						Error:      turn.AgentError,
					}
				}
				messages = append(messages, mistyConversationMessage{
					ID: turn.InvocationID + "-assistant", InvocationID: turn.InvocationID, Artifact: pendingArtifact, Role: "assistant", Mode: mode,
					Content: response, CreatedAt: turn.ReplyAt.UTC().Format(time.RFC3339Nano),
					State:     mistyTurnMessageState(turn.State, turn.AgentState),
					Retryable: turn.State == "failed" || turn.AgentState == "failed", Action: action,
				})
			}
		}
		if notes != nil {
			markCompactedAfter(messages, notes.ThroughInvocationID)
		}
		modelID := agent.FrontierDefaultModelID()
		if summary.ModelOverride != "" && agent.ClientModelChoice() {
			modelID = summary.ModelOverride
		}
		return mistyConversation{
			ID: summary.ID, AgentID: summary.AgentID, FolderID: summary.FolderID, Title: cleanMistyTitle(summary.Title),
			SpaceID: summary.SpaceID, Kind: summary.ConversationKind, OriginSurface: summary.OriginSurface,
			OriginHref: summary.OriginHref, Privacy: summary.PrivacyBoundary, ModelID: modelID, ModelOverride: summary.ModelOverride, Reasoning: agent.ManagedReasoning("", summary.ReasoningEffort),
			CreatedAt: summary.CreatedAt.UTC().Format(time.RFC3339Nano),
			UpdatedAt: summary.UpdatedAt.UTC().Format(time.RFC3339Nano), LastReadAt: formatReadAt(summary.LastReadAt), Messages: messages, Remote: true, KeptOnDeviceID: summary.KeptOnDeviceID,
		}, nil
	}
	transcript, err := s.runtime.Transcript(r.Context(), summary.ID, userID)
	if err != nil {
		return mistyConversation{}, err
	}
	messages := make([]mistyConversationMessage, 0, len(transcript))
	for index, item := range transcript {
		role := "assistant"
		if item.Role == agent.RoleUser {
			role = "user"
		}
		content := strings.TrimSpace(item.Content)
		if role == "user" {
			content = publicMistyConversationContent(content)
		}
		if content == "" {
			continue
		}
		messages = append(messages, mistyConversationMessage{
			ID: fmt.Sprintf("%s-%d", summary.ID, index), Role: role, Mode: "ask",
			Content: content, CreatedAt: summary.UpdatedAt.UTC().Format(time.RFC3339Nano), State: "completed",
		})
	}
	modelID := agent.FrontierDefaultModelID()
	if summary.ModelOverride != "" && agent.ClientModelChoice() {
		modelID = summary.ModelOverride
	}
	return mistyConversation{
		ID: summary.ID, AgentID: summary.AgentID, FolderID: summary.FolderID, Title: cleanMistyTitle(summary.Title),
		SpaceID: summary.SpaceID, Kind: summary.ConversationKind, OriginSurface: summary.OriginSurface,
		OriginHref: summary.OriginHref, Privacy: summary.PrivacyBoundary, ModelID: modelID, ModelOverride: summary.ModelOverride, Reasoning: agent.ManagedReasoning("", summary.ReasoningEffort),
		CreatedAt: summary.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt: summary.UpdatedAt.UTC().Format(time.RFC3339Nano), LastReadAt: formatReadAt(summary.LastReadAt), Messages: messages, Remote: true, KeptOnDeviceID: summary.KeptOnDeviceID,
	}, nil
}

// formatReadAt renders a conversation's read time; never read is empty.
func formatReadAt(readAt *time.Time) string {
	if readAt == nil {
		return ""
	}
	return readAt.UTC().Format(time.RFC3339Nano)
}

// markCompactedAfter flags the last message of the newest summarized turn, so
// the transcript can show where the model's notes end.
func markCompactedAfter(messages []mistyConversationMessage, invocationID string) {
	for index := len(messages) - 1; index >= 0; index-- {
		if strings.HasPrefix(messages[index].ID, invocationID+"-") {
			messages[index].CompactedAfter = true
			return
		}
	}
}

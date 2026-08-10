package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type acceptedSuggestionResult struct {
	Suggestion *db.SpaceActionSuggestionBatch  `json:"suggestion"`
	Runs       []*db.SpaceRun                  `json:"runs"`
	FollowUps  []*db.SpaceConversationFollowUp `json:"follow_ups"`
}

func (s *SpacesService) AcceptActionSuggestion() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, batchID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "batchID")
		var review db.SpaceActionSuggestionAcceptance
		if decodeJSON(w, r, &review) != nil {
			return
		}
		current, err := s.database.SpaceActionSuggestion(r.Context(), userID, spaceID, batchID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		byID := map[string]db.SpaceActionSuggestionItem{}
		for _, item := range current.Items {
			byID[item.ID] = item
		}
		for _, reviewed := range review.Items {
			item, exists := byID[reviewed.ItemID]
			if !exists {
				writeSpaceError(w, db.ErrSpaceInvalid)
				return
			}
			if err := validateReviewedSuggestionInput(item.ActionKind, reviewed.ApprovedInput); err != nil {
				writeSpaceError(w, err)
				return
			}
			if err := s.database.AuthorizeSuggestionAction(r.Context(), userID, spaceID, reviewed.SelectedAgentID, item.RequiredCapability, current.Scope); err != nil {
				writeSpaceError(w, err)
				return
			}
		}
		suggestion, accepted, err := s.database.AcceptSpaceActionSuggestion(r.Context(), userID, spaceID, batchID, review)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		result := acceptedSuggestionResult{Suggestion: suggestion, Runs: []*db.SpaceRun{}, FollowUps: []*db.SpaceConversationFollowUp{}}
		for _, item := range accepted {
			run, followUp, executeErr := s.executeReviewedSuggestion(r.Context(), userID, current, item)
			if run != nil {
				result.Runs = append(result.Runs, run)
			}
			if followUp != nil {
				result.FollowUps = append(result.FollowUps, followUp)
			}
			if executeErr != nil {
				_ = s.database.CompleteSpaceActionSuggestionItem(r.Context(), userID, spaceID, item.ID, "failed", runID(run), "")
				continue
			}
			_ = s.database.CompleteSpaceActionSuggestionItem(r.Context(), userID, spaceID, item.ID, "completed", runID(run), followUpID(followUp))
		}
		updated, _ := s.database.SpaceActionSuggestion(r.Context(), userID, spaceID, batchID)
		if updated != nil {
			result.Suggestion = updated
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func validateReviewedSuggestionInput(kind string, raw json.RawMessage) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return db.ErrSpaceInvalid
	}
	requiredText := func(name string) bool {
		var value string
		return json.Unmarshal(fields[name], &value) == nil && strings.TrimSpace(value) != ""
	}
	if kind == db.SuggestionTaskCreate || kind == db.SuggestionJournalCreate || kind == db.SuggestionRoadmapCreate {
		if !requiredText("title") {
			return db.ErrSpaceInvalid
		}
		return nil
	}
	if kind == db.SuggestionCalendarCreate {
		var startsAt, endsAt time.Time
		var timezone string
		if !requiredText("title") || json.Unmarshal(fields["starts_at"], &startsAt) != nil || json.Unmarshal(fields["ends_at"], &endsAt) != nil || !endsAt.After(startsAt) || json.Unmarshal(fields["timezone"], &timezone) != nil {
			return db.ErrSpaceInvalid
		}
		if _, err := time.LoadLocation(timezone); err != nil {
			return db.ErrSpaceInvalid
		}
		return nil
	}
	if kind == db.SuggestionFollowUpSchedule {
		var deliverAt time.Time
		var recipients []string
		var timezone string
		if !requiredText("reminder_text") || json.Unmarshal(fields["deliver_at"], &deliverAt) != nil || !deliverAt.After(time.Now().UTC()) || json.Unmarshal(fields["recipient_user_ids"], &recipients) != nil || len(recipients) == 0 || json.Unmarshal(fields["timezone"], &timezone) != nil {
			return db.ErrSpaceInvalid
		}
		if _, err := time.LoadLocation(timezone); err != nil {
			return db.ErrSpaceInvalid
		}
		return nil
	}
	return db.ErrSpaceInvalid
}

func runID(run *db.SpaceRun) string {
	if run == nil {
		return ""
	}
	return run.ID
}
func followUpID(item *db.SpaceConversationFollowUp) string {
	if item == nil {
		return ""
	}
	return item.ID
}

func suggestionAudience(batch *db.SpaceActionSuggestionBatch) db.SpaceResourceAudience {
	if batch.Scope.Kind == db.ConversationScopePrivate {
		return db.SpaceResourceAudience{Kind: db.SpaceAudienceConversation, ConversationID: batch.Scope.ConversationID}
	}
	return db.SpaceResourceAudience{Kind: db.SpaceAudienceSpace}
}

func (s *SpacesService) executeReviewedSuggestion(ctx context.Context, userID string, batch *db.SpaceActionSuggestionBatch, item db.SpaceActionSuggestionItem) (*db.SpaceRun, *db.SpaceConversationFollowUp, error) {
	if err := s.database.AuthorizeSuggestionAction(ctx, userID, batch.SpaceID, item.SelectedAgentID, item.RequiredCapability, batch.Scope); err != nil {
		return nil, nil, err
	}
	source := batch.AnchorMessageID
	if batch.Scope.Kind == db.ConversationScopePrivate {
		source = batch.Scope.ConversationID
	}
	envelope := TestingMustAPIRawJSON(map[string]any{"suggestion_batch_id": batch.ID, "suggestion_item_id": item.ID, "locked_tool": item.ActionKind, "locked_payload": json.RawMessage(item.ApprovedInput), "source_message_id": batch.AnchorMessageID})
	run, err := s.database.CreatePersonalAgentSpaceRun(ctx, userID, batch.SpaceID, item.SelectedAgentID, source, "suggestion", "suggestion", item.ApprovedInput, envelope)
	if err != nil {
		return nil, nil, err
	}
	var artifact any
	var followUp *db.SpaceConversationFollowUp
	audience := suggestionAudience(batch)
	switch item.ActionKind {
	case db.SuggestionTaskCreate:
		var input struct {
			Title          string     `json:"title"`
			Notes          string     `json:"notes"`
			Priority       string     `json:"priority"`
			DueAt          *time.Time `json:"due_at"`
			DueTimezone    string     `json:"due_timezone"`
			AssigneeUserID string     `json:"assignee_user_id"`
		}
		err = json.Unmarshal(item.ApprovedInput, &input)
		if err == nil {
			artifact, err = s.database.CreateSpaceTask(ctx, userID, db.SpaceTask{SpaceID: batch.SpaceID, Title: input.Title, Notes: input.Notes, Status: "todo", Priority: input.Priority, DueAt: input.DueAt, DueTimezone: input.DueTimezone, AssigneeUserID: input.AssigneeUserID, CreatedByUserID: userID, CreatedByAgentID: item.SelectedAgentID, SourceRunID: run.ID, AudienceKind: audience.Kind, AudienceConversationID: audience.ConversationID, AudienceCreatorUserID: userID})
		}
	case db.SuggestionCalendarCreate:
		var input reviewedCalendarEventInput
		err = json.Unmarshal(item.ApprovedInput, &input)
		if err == nil && batch.Scope.Kind == db.ConversationScopePrivate && input.CalendarSourceID != "" && input.CalendarSourceID != "misty" {
			err = db.ErrSpaceForbidden
		}
		if err == nil && (input.CalendarSourceID == "" || input.CalendarSourceID == "misty") {
			artifact, err = s.database.CreateNativeCalendarEvent(ctx, userID, db.SpaceCalendarEvent{SpaceID: batch.SpaceID, Title: input.Title, Description: input.Description, Location: input.Location, StartsAt: input.StartsAt, EndsAt: input.EndsAt, AllDay: input.AllDay, Timezone: input.Timezone, Status: "confirmed", AudienceKind: audience.Kind, AudienceConversationID: audience.ConversationID, CreatedByUserID: userID, CreatedByAgentID: item.SelectedAgentID, SourceRunID: run.ID})
		} else if err == nil {
			artifact, err = s.createReviewedExternalCalendarEvent(ctx, userID, batch.SpaceID, input)
		}
	case db.SuggestionJournalCreate:
		var input struct {
			Title    string `json:"title"`
			Markdown string `json:"markdown"`
		}
		err = json.Unmarshal(item.ApprovedInput, &input)
		if err == nil {
			artifact, err = s.database.CreateSpaceNoteWithAudience(ctx, userID, batch.SpaceID, input.Title, audience, input.Markdown)
		}
	case db.SuggestionRoadmapCreate:
		var input struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			RoadmapID   string `json:"roadmap_id"`
		}
		err = json.Unmarshal(item.ApprovedInput, &input)
		if err == nil && input.RoadmapID == "" {
			artifact, err = s.database.CreateSpaceRoadmapWithAudience(ctx, userID, batch.SpaceID, input.Title, input.Description, audience)
		} else if err == nil {
			var snapshot *db.SpaceRoadmapSnapshot
			snapshot, err = s.database.SpaceRoadmap(ctx, userID, batch.SpaceID, input.RoadmapID)
			if err == nil && (snapshot.Roadmap.AudienceKind != audience.Kind || snapshot.Roadmap.AudienceConversationID != audience.ConversationID) {
				err = db.ErrSpaceForbidden
			}
			if err == nil {
				var version int64
				artifact, version, err = s.database.CreateSpaceRoadmapNode(ctx, userID, batch.SpaceID, input.RoadmapID, db.SpaceRoadmapNode{NodeKind: "note", Title: input.Title, Description: input.Description, FieldValues: json.RawMessage(`{}`)}, snapshot.Roadmap.GraphVersion)
				_ = version
			}
		}
	case db.SuggestionFollowUpSchedule:
		var input struct {
			ReminderText     string    `json:"reminder_text"`
			DeliverAt        time.Time `json:"deliver_at"`
			Timezone         string    `json:"timezone"`
			RecipientUserIDs []string  `json:"recipient_user_ids"`
		}
		err = json.Unmarshal(item.ApprovedInput, &input)
		if err == nil {
			followUp, err = s.database.CreateConversationFollowUp(ctx, userID, db.SpaceConversationFollowUp{SpaceID: batch.SpaceID, SourceScope: batch.Scope, SourceMessageID: batch.AnchorMessageID, AgentID: item.SelectedAgentID, ReminderText: input.ReminderText, DeliverAt: input.DeliverAt, Timezone: input.Timezone, RecipientUserIDs: input.RecipientUserIDs}, item.ID)
			artifact = followUp
		}
	default:
		err = db.ErrSpaceInvalid
	}
	if err != nil {
		_, _ = s.database.FinishSpaceRun(ctx, run.ID, "failed", TestingMustAPIRawJSON(map[string]string{"message": err.Error()}), "suggestion_action_failed")
		_, _ = s.createConversationAgentMessage(ctx, userID, batch.SpaceID, batch.Scope.ConversationID, item.SelectedAgentID, "I couldn't complete the reviewed action: "+item.Title)
		return run, followUp, err
	}
	result := TestingMustAPIRawJSON(map[string]any{"suggestion_item_id": item.ID, "action_kind": item.ActionKind, "artifact": artifact})
	completed, finishErr := s.database.FinishSpaceRun(ctx, run.ID, "completed", result, "")
	if finishErr == nil {
		run = completed
		_, _ = s.createConversationAgentMessage(ctx, userID, batch.SpaceID, batch.Scope.ConversationID, item.SelectedAgentID, fmt.Sprintf("Completed suggested action: %s", strings.TrimSpace(item.Title)))
	}
	return run, followUp, finishErr
}

package api

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (s *SpacesService) ProcessConversationFollowUps(ctx context.Context, limit int) (int, error) {
	items, err := s.database.LeaseDueConversationFollowUps(ctx, limit)
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, item := range items {
		recipients, recipientErr := s.database.ConversationFollowUpRecipients(ctx, item.ID)
		if recipientErr != nil {
			continue
		}
		if err := s.database.AuthorizeSuggestionAction(ctx, item.AuthorizingUserID, item.SpaceID, item.AgentID, "conversation.follow_up.schedule", item.SourceScope); err != nil {
			for _, recipient := range recipients {
				_ = s.database.FinishConversationFollowUpRecipient(ctx, item.ID, recipient, "failed", "", "", "authorization_revoked")
			}
			processed++
			continue
		}
		for _, recipient := range recipients {
			eligible, eligibilityErr := s.database.ConversationFollowUpRecipientEligible(ctx, item, recipient)
			if eligibilityErr != nil || !eligible {
				_ = s.database.FinishConversationFollowUpRecipient(ctx, item.ID, recipient, "skipped", "", "", "recipient_removed")
				continue
			}
			conversation, conversationErr := s.database.DirectAgentConversation(ctx, recipient, item.SpaceID, item.AgentID)
			if conversationErr != nil {
				_ = s.database.FinishConversationFollowUpRecipient(ctx, item.ID, recipient, "failed", "", "", "direct_conversation_failed")
				continue
			}
			message, messageErr := s.database.CreateSpaceConversationAgentMessageWithSourceLink(ctx, recipient, item.SpaceID, conversation.ID, item.AgentID, item.ReminderText, item.SourceScope.ConversationID)
			if messageErr != nil {
				_ = s.database.FinishConversationFollowUpRecipient(ctx, item.ID, recipient, "failed", conversation.ID, "", "delivery_failed")
				continue
			}
			_ = s.database.FinishConversationFollowUpRecipient(ctx, item.ID, recipient, "delivered", conversation.ID, message.ID, "")
		}
		processed++
	}
	return processed, nil
}

func (s *SpacesService) CancelConversationFollowUp() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if err := s.database.CancelConversationFollowUp(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "followUpID")); err != nil {
			writeSpaceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *SpacesService) OptOutConversationFollowUp() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if err := s.database.OptOutConversationFollowUp(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "followUpID")); err != nil {
			writeSpaceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

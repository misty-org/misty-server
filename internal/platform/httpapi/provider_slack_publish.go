package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func (s *SpacesService) PublishSpaceSlackMessage() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, linkID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "linkID")
		var body struct {
			MessageID string `json:"message_id"`
			ThreadTS  string `json:"thread_ts"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		if len(strings.TrimSpace(body.ThreadTS)) > 64 {
			writeSpaceError(w, db.ErrSpaceInvalid)
			return
		}
		if err := s.database.RequireSpacePermission(r.Context(), userID, spaceID, db.PermissionIntegrationsManage); err != nil {
			writeSpaceError(w, err)
			return
		}
		links, err := s.database.SpaceSlackLinksFor(r.Context(), userID, spaceID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		var link *db.SpaceSlackLink
		for index := range links {
			if links[index].ID == linkID {
				link = &links[index]
				break
			}
		}
		if link == nil {
			writeSpaceError(w, db.ErrSpaceNotFound)
			return
		}
		if link.Direction == "inbound" {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "outbound_disabled"})
			return
		}
		message, err := s.database.SpaceMessageForPublish(r.Context(), userID, spaceID, body.MessageID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if message.ConversationID != link.ConversationID || !TestingPublishableToSlack(message, userID) {
			writeSpaceError(w, db.ErrSpaceForbidden)
			return
		}
		if len(message.Origin) > 0 {
			var previous db.MessageOrigin
			if json.Unmarshal(message.Origin, &previous) == nil && previous.System == "misty" &&
				previous.PublishState == "published" && previous.PublishedExternal != "" {
				writeJSON(w, http.StatusOK, map[string]any{"message": message, "idempotent_replay": true})
				return
			}
		}
		token, tokenType, err := s.providerAccessToken(r.Context(), link.ConnectedByUserID,
			link.SpaceID, link.IntegrationID)
		if err != nil {
			writeProviderFailure(w, err)
			return
		}
		claimed, err := s.database.ClaimSpaceMessageDiscordPublish(r.Context(), userID,
			spaceID, message.ID, link.ChannelID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if !claimed {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "publish_in_progress_or_complete"})
			return
		}
		text := strings.TrimSpace(TestingSpansToPlainText(message.Content))
		origin := db.MessageOrigin{System: "misty", ExternalChannelID: link.ChannelID,
			ExternalThreadID: strings.TrimSpace(body.ThreadTS), PublishState: "published"}
		externalID, publishErr := s.slackChatProvider(token, tokenType).Post(r.Context(),
			link.ChannelID, text, origin.ExternalThreadID, message.ID)
		if publishErr != nil {
			origin.PublishState = "failed"
			origin.PublishError = providerErrorCode(publishErr)
		} else {
			origin.PublishedExternal = externalID
			origin.PublishedAt = time.Now().UTC().Format(time.RFC3339)
		}
		updated, err := s.database.SetSpaceMessageOrigin(r.Context(), spaceID, message.ID, origin)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"message": updated})
	}
}

func TestingPublishableToSlack(message *db.SpaceMessage, userID string) bool {
	if message == nil || message.SenderKind != "person" || message.SenderUserID != userID {
		return false
	}
	if len(message.Origin) > 0 {
		var origin db.MessageOrigin
		if json.Unmarshal(message.Origin, &origin) == nil && origin.System != "" && origin.System != "misty" {
			return false
		}
	}
	text := []rune(strings.TrimSpace(TestingSpansToPlainText(message.Content)))
	return len(text) > 0 && len(text) <= 4000
}

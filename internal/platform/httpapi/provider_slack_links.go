package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func (s *SpacesService) SpaceSlackLinks() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		if r.Method == http.MethodGet {
			links, err := s.database.SpaceSlackLinksFor(r.Context(), userID, spaceID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"links": links})
			return
		}
		var body struct {
			IntegrationID  string `json:"integration_id"`
			ChannelID      string `json:"channel_id"`
			ConversationID string `json:"conversation_id"`
			Direction      string `json:"direction"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		if err := s.database.RequireSpacePermission(r.Context(), userID, spaceID, db.PermissionIntegrationsManage); err != nil {
			writeSpaceError(w, err)
			return
		}
		token, tokenType, err := s.providerAccessToken(r.Context(), userID, spaceID, body.IntegrationID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		provider := s.slackChatProvider(token, tokenType)
		identity, err := provider.Identity(r.Context())
		if err != nil {
			writeProviderFailure(w, err)
			return
		}
		channel, err := provider.Channel(r.Context(), strings.TrimSpace(body.ChannelID))
		if err != nil || channel.ID != strings.TrimSpace(body.ChannelID) {
			writeProviderFailure(w, firstError(err, db.ErrSpaceInvalid))
			return
		}
		configuration, _ := json.Marshal(map[string]any{"accountId": identity.TeamID,
			"teamName": identity.TeamName})
		resource, err := s.database.PublishProviderSharedResource(r.Context(), userID, db.ProviderSharedResource{
			SpaceID: spaceID, IntegrationID: body.IntegrationID, Provider: "slack", ResourceType: "channel",
			ExternalResourceID: channel.ID, DisplayName: "#" + strings.TrimPrefix(channel.Name, "#"),
			Configuration: configuration})
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		link, err := s.database.CreateSpaceSlackLink(r.Context(), userID, db.SpaceSlackLink{
			SpaceID: spaceID, IntegrationID: body.IntegrationID, SharedResourceID: resource.ID,
			ConversationID: body.ConversationID, ChannelID: channel.ID, ChannelName: channel.Name,
			Direction: body.Direction, BotUserID: identity.UserID})
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		imported, syncErr := s.syncSlackLink(r.Context(), link)
		if syncErr != nil {
			_ = s.database.SetSpaceSlackLinkSync(r.Context(), link.ID, "", "needs_attention",
				providerErrorCode(syncErr), identity.UserID, nil)
		}
		refreshed, _ := s.database.SpaceSlackLinkByID(r.Context(), spaceID, link.ID)
		writeJSON(w, http.StatusCreated, map[string]any{"link": refreshed, "imported": imported})
	}
}

func (s *SpacesService) SpaceSlackLink() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, linkID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "linkID")
		if r.Method == http.MethodDelete {
			if err := s.database.DeleteSpaceSlackLink(r.Context(), userID, spaceID, linkID); err != nil {
				writeSpaceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var body struct {
			Direction string `json:"direction"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		link, err := s.database.UpdateSpaceSlackLinkDirection(r.Context(), userID, spaceID, linkID, body.Direction)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"link": link})
	}
}

func (s *SpacesService) SyncSpaceSlackLink() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, linkID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "linkID")
		links, err := s.database.SpaceSlackLinksFor(r.Context(), userID, spaceID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		var link *db.SpaceSlackLink
		for index := range links {
			if links[index].ID == linkID {
				link = &links[index]
			}
		}
		if link == nil {
			writeSpaceError(w, db.ErrSpaceNotFound)
			return
		}
		imported, err := s.syncSlackLink(r.Context(), link)
		if err != nil {
			_ = s.database.SetSpaceSlackLinkSync(r.Context(), link.ID, "", "needs_attention",
				providerErrorCode(err), "", nil)
			writeProviderFailure(w, err)
			return
		}
		refreshed, _ := s.database.SpaceSlackLinkByID(r.Context(), spaceID, link.ID)
		writeJSON(w, http.StatusOK, map[string]any{"link": refreshed, "imported": imported})
	}
}

func (s *SpacesService) slackChatProvider(token, tokenType string) SlackChatProvider {
	factory := s.slackChatProviderFactory
	if factory == nil {
		factory = defaultSlackChatProviderFactory
	}
	return factory(token, tokenType)
}

func firstError(primary, fallback error) error {
	if primary != nil {
		return primary
	}
	return fallback
}

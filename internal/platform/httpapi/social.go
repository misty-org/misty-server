package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	socialintegration "github.com/kannachi323/misty/server/internal/integrations/social"
	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	"github.com/go-chi/chi/v5"
)

func (s *SpacesService) SocialProviders() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := authenticatedUser(w, r, s.database); !ok {
			return
		}
		providers := []map[string]any{
			{"id": "misty", "name": "Misty", "configured": true, "capabilities": socialintegration.MistyAdapter{}.Capabilities()},
			{"id": "instagram", "name": "Instagram", "configured": connectedAccountClientID(TestingConnectedAccountOAuthCatalog["instagram"]) != "" && connectedAccountClientSecret(TestingConnectedAccountOAuthCatalog["instagram"]) != "", "capabilities": socialintegration.InstagramAdapter{}.Capabilities()},
			{"id": "discord", "name": "Discord", "configured": connectedAccountClientID(TestingConnectedAccountOAuthCatalog["discord"]) != "" && connectedAccountClientSecret(TestingConnectedAccountOAuthCatalog["discord"]) != "" && strings.TrimSpace(envconfig.Getenv("DISCORD_BOT_TOKEN")) != "", "capabilities": socialintegration.DiscordAdapter{}.Capabilities()},
		}
		writeJSON(w, http.StatusOK, map[string]any{"providers": providers})
	}
}

func (s *SpacesService) SocialResources() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		connectionID := chi.URLParam(r, "connectionID")
		connection, err := s.database.ConnectedAccount(r.Context(), userID, connectionID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		token, _, err := s.connectedAccountAccessTokenForCapability(r.Context(), userID, connectionID, "social_read")
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		var adapter socialintegration.SocialProviderAdapter
		switch connection.Provider {
		case "discord":
			adapter = socialintegration.DiscordAdapter{
				BotToken: strings.TrimSpace(envconfig.Getenv("DISCORD_BOT_TOKEN")),
			}
		case "instagram":
			adapter = socialintegration.InstagramAdapter{
				APIBase: strings.TrimSpace(envconfig.Getenv("INSTAGRAM_GRAPH_API_BASE_URL")),
			}
		default:
			writeSpaceError(w, db.ErrSpaceInvalid)
			return
		}
		resources, err := adapter.DiscoverResources(r.Context(), token)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"code": "social_discovery_failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"provider": connection.Provider, "resources": resources})
	}
}

func (s *SpacesService) SocialBindings() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		if r.Method == http.MethodGet {
			items, err := s.database.SocialBindings(r.Context(), userID, spaceID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"bindings": items})
			return
		}
		var body struct{ ConnectionID, Provider, ExternalResourceID, ExternalParentID, DisplayName string }
		if decodeJSON(w, r, &body) != nil {
			return
		}
		item, err := s.database.CreateSocialBinding(r.Context(), userID, spaceID, body.ConnectionID, strings.ToLower(body.Provider), body.ExternalResourceID, body.ExternalParentID, body.DisplayName)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, item)
	}
}

func (s *SpacesService) SocialBinding() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if err := s.database.DisableSocialBinding(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "bindingID")); err != nil {
			writeSpaceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *SpacesService) SocialSendAuthorities() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		if r.Method == http.MethodGet {
			items, err := s.database.SocialSendAuthorities(r.Context(), userID, spaceID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"authorities": items})
			return
		}
		var body struct {
			ConnectionID    string `json:"connection_id"`
			BindingID       string `json:"binding_id"`
			AllowManual     bool   `json:"allow_manual"`
			AllowScheduled  bool   `json:"allow_scheduled"`
			AllowAutomation bool   `json:"allow_automation"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		item, err := s.database.UpsertSocialSendAuthority(r.Context(), userID, spaceID, body.ConnectionID, body.BindingID, body.AllowManual, body.AllowScheduled, body.AllowAutomation)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	}
}

func (s *SpacesService) SocialAutomationRules() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		if r.Method == http.MethodGet {
			items, err := s.database.SocialAutomationRules(r.Context(), userID, spaceID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"rules": items})
			return
		}
		var body db.SocialAutomationRule
		if decodeJSON(w, r, &body) != nil {
			return
		}
		item, err := s.database.SaveSocialAutomationRule(r.Context(), userID, spaceID, body)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, item)
	}
}

func (s *SpacesService) SocialScheduledMessages() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		if r.Method == http.MethodGet {
			items, err := s.database.SocialScheduledMessages(r.Context(), userID, spaceID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"scheduled_messages": items})
			return
		}
		var body struct {
			BindingID      string          `json:"binding_id"`
			ConversationID string          `json:"conversation_id"`
			AuthorityID    string          `json:"authority_id"`
			Content        json.RawMessage `json:"content"`
			ScheduledAt    time.Time       `json:"scheduled_at"`
			Timezone       string          `json:"timezone"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		item, err := s.database.ScheduleSocialMessage(r.Context(), userID, spaceID, db.SocialScheduledMessage{BindingID: body.BindingID, ConversationID: body.ConversationID, AuthorityID: body.AuthorityID, Content: body.Content, ScheduledAt: body.ScheduledAt, Timezone: body.Timezone})
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, item)
	}
}

func (s *SpacesService) SocialScheduledMessage() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if err := s.database.CancelSocialScheduledMessage(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "scheduledID")); err != nil {
			writeSpaceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

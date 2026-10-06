package api

import (
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kannachi323/misty/server/internal/integrations/composio"
)

// appsUser authenticates the account owner. Extension and app authority can
// never manage connections or answer an agent's request.
func (s *SpacesService) appsUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	return authenticatedUser(w, r, s.database)
}

func writeAppsError(w http.ResponseWriter, err error) {
	var apiErr *composio.APIError
	switch {
	case errors.Is(err, composio.ErrUnavailable):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "apps_unavailable", "message": "Connected apps are not set up on this Misty server."})
	case composio.Rejected(err) && errors.As(err, &apiErr):
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "apps_rejected", "message": apiErr.Message})
	case errors.As(err, &apiErr), errors.Is(err, composio.ErrTransport):
		writeJSON(w, http.StatusBadGateway, map[string]string{"code": "apps_unreachable", "message": "Misty couldn't reach the connected apps service. Try again."})
	default:
		writeSpaceError(w, err)
	}
}

// abandonedSignIn is how long an unfinished sign-in stays listed. Each
// sign-in link starts a new account, so closed browser tabs leave these behind.
const abandonedSignIn = 15 * time.Minute

type connectedApp struct {
	ID        string `json:"id"`
	App       string `json:"app"`
	Name      string `json:"name"`
	Alias     string `json:"alias,omitempty"`
	Account   string `json:"account,omitempty"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

// ConnectedApps lists the account's connected apps.
func (s *SpacesService) ConnectedApps() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.appsUser(w, r)
		if !ok {
			return
		}
		if !appsAvailable() {
			writeJSON(w, http.StatusOK, map[string]any{"available": false, "apps": []connectedApp{}})
			return
		}
		names := map[string]string{}
		var accounts []composio.Account
		var identities map[string]string
		err := s.withAppsSession(r.Context(), user, func(client *composio.Client, session string) error {
			toolkits, _, err := client.Toolkits(r.Context(), session, "", "", true)
			if err != nil {
				return err
			}
			for _, toolkit := range toolkits {
				names[toolkit.Slug] = toolkit.Name
			}
			accounts, err = client.Accounts(r.Context(), composio.UserID(user), "")
			if err != nil {
				return err
			}
			identities = client.Identities(r.Context(), session, accounts)
			return nil
		})
		if err != nil {
			writeAppsError(w, err)
			return
		}
		apps := []connectedApp{}
		for _, account := range accounts {
			status := "needs_attention"
			if account.Active() {
				status = "active"
			} else if account.Status == "INITIATED" || account.Status == "INITIALIZING" {
				if started, err := time.Parse(time.RFC3339, account.CreatedAt); err == nil && time.Since(started) > abandonedSignIn {
					continue
				}
				status = "pending"
			}
			name := names[account.Toolkit.Slug]
			if name == "" {
				name = account.Toolkit.Slug
			}
			apps = append(apps, connectedApp{ID: account.ID, App: account.Toolkit.Slug, Name: name, Alias: account.Alias, Account: identities[account.ID], Status: status, CreatedAt: account.CreatedAt})
		}
		sort.SliceStable(apps, func(i, j int) bool { return strings.ToLower(apps[i].Name) < strings.ToLower(apps[j].Name) })
		writeJSON(w, http.StatusOK, map[string]any{"available": true, "apps": apps})
	}
}

// AppCatalog searches every app a user can connect.
func (s *SpacesService) AppCatalog() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.appsUser(w, r)
		if !ok {
			return
		}
		search, cursor := strings.TrimSpace(r.URL.Query().Get("search")), r.URL.Query().Get("cursor")
		if len(search) > 100 || len(cursor) > 500 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_search"})
			return
		}
		type catalogApp struct {
			App         string `json:"app"`
			Name        string `json:"name"`
			Description string `json:"description"`
			Connected   bool   `json:"connected"`
		}
		apps := []catalogApp{}
		next := ""
		err := s.withAppsSession(r.Context(), user, func(client *composio.Client, session string) error {
			items, nextCursor, err := client.Toolkits(r.Context(), session, search, cursor, false)
			for _, item := range items {
				apps = append(apps, catalogApp{App: item.Slug, Name: item.Name, Description: truncateAgentRuntimeText(item.Meta.Description, 240), Connected: item.Connected()})
			}
			next = nextCursor
			return err
		})
		if err != nil {
			writeAppsError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"apps": apps, "next_cursor": next})
	}
}

// ConnectApp returns a sign-in link for one app.
func (s *SpacesService) ConnectApp() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.appsUser(w, r)
		if !ok {
			return
		}
		var body struct {
			App string `json:"app"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		s.writeAppLink(w, r, user, strings.ToLower(strings.TrimSpace(body.App)))
	}
}

func (s *SpacesService) writeAppLink(w http.ResponseWriter, r *http.Request, user, app string) {
	if !composio.ValidToolkit(app) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_app"})
		return
	}
	link := ""
	err := s.withAppsSession(r.Context(), user, func(client *composio.Client, session string) error {
		var err error
		link, err = client.Link(r.Context(), session, app)
		return err
	})
	if err != nil {
		writeAppsError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"url": link})
}

// DisconnectApp removes one connected account from Composio.
func (s *SpacesService) DisconnectApp() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.appsUser(w, r)
		if !ok {
			return
		}
		client, err := composioClient()
		if err == nil {
			err = client.DeleteAccount(r.Context(), composio.UserID(user), chi.URLParam(r, "accountID"))
		}
		if err != nil {
			writeAppsError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// AppRequestControl reads a chat card or records the user's answer to it.
func (s *SpacesService) AppRequestControl() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.appsUser(w, r)
		if !ok {
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		id := chi.URLParam(r, "requestID")
		if r.Method == http.MethodGet {
			request, err := s.database.AgentAppRequest(r.Context(), user, id)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			request = s.refreshConnectRequest(r.Context(), user, request)
			writeJSON(w, http.StatusOK, map[string]any{"request": appRequestView(request)})
			return
		}
		var body struct {
			Decision string `json:"decision"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		state := map[string]string{"approve": "approved", "decline": "declined"}[body.Decision]
		if state == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_decision"})
			return
		}
		request, err := s.database.DecideAgentAppRequest(r.Context(), user, id, state)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		s.showAppRequest(r.Context(), user, request.RunID, request)
		writeJSON(w, http.StatusOK, map[string]any{"request": appRequestView(request)})
	}
}

// AppRequestLink opens sign-in for a pending Connect card.
func (s *SpacesService) AppRequestLink() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.appsUser(w, r)
		if !ok {
			return
		}
		request, err := s.database.AgentAppRequest(r.Context(), user, chi.URLParam(r, "requestID"))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if request.Kind != "connect" {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "not_a_connect_request"})
			return
		}
		s.writeAppLink(w, r, user, request.Subject)
	}
}

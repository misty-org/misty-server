package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	mailintegration "github.com/kannachi323/misty/server/internal/integrations/mail"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type mailAccountDTO struct {
	ConnectionID string `json:"connection_id"`
	Provider     string `json:"provider"`
	AccountID    string `json:"account_id"`
	Email        string `json:"email"`
	DisplayName  string `json:"display_name"`
	Total        int64  `json:"total"`
	Unread       int64  `json:"unread"`
	Status       string `json:"status,omitempty"`
	ErrorCode    string `json:"error_code,omitempty"`
}

func (s *SpacesService) MailAccounts() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		connections, err := s.database.ConnectedAccounts(r.Context(), userID)
		if err != nil {
			writeMailError(w, err)
			return
		}
		accounts := []mailAccountDTO{}
		for _, connection := range connections {
			if (connection.Provider != "google" && connection.Provider != "microsoft") || !containsString(connection.Capabilities, "mail") {
				continue
			}
			provider, _, err := s.mailProvider(r.Context(), userID, connection.ID)
			if err != nil {
				accounts = append(accounts, fallbackMailAccount(connection, err))
				continue
			}
			profile, err := provider.Account(r.Context())
			if err != nil {
				accounts = append(accounts, fallbackMailAccount(connection, err))
				continue
			}
			// A Microsoft identity can exist without an Exchange/Outlook mailbox
			// (for example, a personal Microsoft login backed by a Gmail address).
			// Probe once here so Inbox can keep the connection visible without
			// repeatedly issuing folder and message requests that can never work.
			if connection.Provider == "microsoft" {
				if _, err := provider.ListFolders(r.Context()); err != nil {
					accounts = append(accounts, fallbackMailAccount(connection, err))
					continue
				}
			}
			displayName := strings.TrimSpace(profile.DisplayName)
			if displayName == "" {
				displayName = connection.AccountDisplay
			}
			accounts = append(accounts, mailAccountDTO{ConnectionID: connection.ID,
				Provider: connection.Provider, AccountID: profile.AccountID, Email: profile.Email,
				DisplayName: displayName, Total: profile.Total, Unread: profile.Unread})
		}
		writeJSON(w, http.StatusOK, map[string]any{"accounts": accounts})
	}
}

func fallbackMailAccount(connection db.ConnectedAccount, err error) mailAccountDTO {
	email := ""
	if strings.Contains(connection.AccountDisplay, "@") {
		email = connection.AccountDisplay
	}
	status := connection.Status
	if status == "" || status == "active" {
		status = "needs_attention"
	}
	errorCode := connection.LastErrorCode
	if errorCode == "" {
		errorCode = mailErrorCode(err)
	}
	return mailAccountDTO{ConnectionID: connection.ID, Provider: connection.Provider,
		AccountID: connection.AccountID, Email: email, DisplayName: connection.AccountDisplay,
		Status: status, ErrorCode: errorCode}
}

func (s *SpacesService) MailFolders() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		provider, _, err := s.mailProvider(r.Context(), userID, r.URL.Query().Get("connection_id"))
		if err != nil {
			writeMailError(w, err)
			return
		}
		folders, err := provider.ListFolders(r.Context())
		if err != nil {
			writeMailError(w, err)
			return
		}
		result := make([]mailFolderDTO, len(folders))
		for index, folder := range folders {
			result[index] = mailFolderToDTO(folder)
		}
		writeJSON(w, http.StatusOK, map[string]any{"folders": result})
	}
}

func (s *SpacesService) MailThreads() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		query := r.URL.Query()
		input := mailintegration.ListThreadsRequest{
			PageToken: strings.TrimSpace(query.Get("page_token")),
			Query:     strings.TrimSpace(query.Get("query")),
			PageSize:  50,
		}
		if folderID := strings.TrimSpace(query.Get("folder_id")); folderID != "" {
			input.FolderIDs = []string{folderID}
		}
		if raw := strings.TrimSpace(query.Get("page_size")); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil {
				writeMailError(w, db.ErrSpaceInvalid)
				return
			}
			input.PageSize = value
		}
		if input.PageSize < 1 || input.PageSize > 100 || len(input.Query) > 2000 ||
			len(input.PageToken) > 4096 || (len(input.FolderIDs) == 1 && len(input.FolderIDs[0]) > 320) {
			writeMailError(w, db.ErrSpaceInvalid)
			return
		}
		provider, _, err := s.mailProvider(r.Context(), userID, query.Get("connection_id"))
		if err != nil {
			writeMailError(w, err)
			return
		}
		page, err := provider.ListThreads(r.Context(), input)
		if err != nil {
			writeMailError(w, err)
			return
		}
		threads := make([]mailThreadDTO, len(page.Threads))
		for index, thread := range page.Threads {
			threads[index] = mailThreadToDTO(thread)
		}
		writeJSON(w, http.StatusOK, map[string]any{"threads": threads,
			"next_page_token": page.NextPageToken, "estimated_total": page.EstimatedTotal})
	}
}

func (s *SpacesService) MailThread() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		threadID := strings.TrimSpace(chi.URLParam(r, "threadID"))
		if threadID == "" || len(threadID) > 320 {
			writeMailError(w, db.ErrSpaceInvalid)
			return
		}
		provider, _, err := s.mailProvider(r.Context(), userID, r.URL.Query().Get("connection_id"))
		if err != nil {
			writeMailError(w, err)
			return
		}
		thread, err := provider.GetThread(r.Context(), threadID)
		if err != nil {
			writeMailError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"thread": mailThreadToDTO(thread)})
	}
}

package api

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

const maxConversationFolderName = 80

// conversationFolderName trims a folder name; ok is false when it is empty or too long.
func conversationFolderName(raw string) (string, bool) {
	name := strings.Join(strings.Fields(raw), " ")
	return name, name != "" && utf8.RuneCountInString(name) <= maxConversationFolderName
}

func writeConversationFolderError(w http.ResponseWriter, err error) {
	if errors.Is(err, db.ErrConversationFolderNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "folder_not_found", "message": "That folder no longer exists."})
		return
	}
	TestingWriteAIError(w, err)
}

func writeInvalidFolderName(w http.ResponseWriter) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_folder_name", "message": "Name the folder in 80 characters or fewer."})
}

// MistyConversationFolders lists the account's folders and creates one for an agent.
func (s *AIService) MistyConversationFolders() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		switch r.Method {
		case http.MethodGet:
			folders, err := s.database.ListAgentConversationFolders(r.Context(), userID)
			if err != nil {
				TestingWriteAIError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"folders": folders})
		case http.MethodPost:
			var body struct {
				AgentID string `json:"agent_id"`
				Name    string `json:"name"`
			}
			if err := decodeAIJSON(w, r, &body); err != nil {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			name, valid := conversationFolderName(body.Name)
			if !valid {
				writeInvalidFolderName(w)
				return
			}
			folder, err := s.database.CreateAgentConversationFolder(r.Context(), userID, strings.TrimSpace(body.AgentID), name)
			if err != nil {
				writeConversationFolderError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, folder)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

// MistyConversationFolder renames or deletes one folder. Deleting returns its
// conversations to Recents; it never deletes them.
func (s *AIService) MistyConversationFolder() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		folderID := strings.TrimSpace(chi.URLParam(r, "folderID"))
		switch r.Method {
		case http.MethodPatch:
			var body struct {
				Name string `json:"name"`
			}
			if err := decodeAIJSON(w, r, &body); err != nil {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			name, valid := conversationFolderName(body.Name)
			if !valid {
				writeInvalidFolderName(w)
				return
			}
			folder, err := s.database.RenameAgentConversationFolder(r.Context(), userID, folderID, name)
			if err != nil {
				writeConversationFolderError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, folder)
		case http.MethodDelete:
			if err := s.database.DeleteAgentConversationFolder(r.Context(), userID, folderID); err != nil {
				writeConversationFolderError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

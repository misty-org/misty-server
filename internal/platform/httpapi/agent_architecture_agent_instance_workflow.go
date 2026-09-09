package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func (s *SpacesService) WorkflowRuns() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		items, err := s.database.SpaceWorkflowRuns(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "workflowID"), 100)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"runs": items})
	}
}

func (s *SpacesService) RunDetail() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		runID := chi.URLParam(r, "runID")
		run, err := s.database.SpaceRun(r.Context(), userID, runID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		actions, _ := s.database.RunActions(r.Context(), userID, runID)
		approvals, _ := s.database.RunApprovals(r.Context(), userID, runID)
		steps, _ := s.database.WorkflowRunSteps(r.Context(), userID, runID)
		writeJSON(w, http.StatusOK, map[string]any{"run": run, "actions": actions, "approvals": approvals, "steps": steps})
	}
}

type personalAgentChatRetry struct {
	ConversationID string
	Content        []db.MessageSpan
	FileNodeIDs    []string
	AttachmentIDs  []string
	LibraryItemIDs []string
}

package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func (s *SpacesService) SpaceTasks() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		switch r.Method {
		case http.MethodGet:
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			dueFrom, dueFromErr := optionalRFC3339(r.URL.Query().Get("due_from"))
			dueTo, dueToErr := optionalRFC3339(r.URL.Query().Get("due_to"))
			if dueFromErr != nil || dueToErr != nil || dueFrom != nil && dueTo != nil && !dueTo.After(*dueFrom) {
				writeSpaceError(w, db.ErrSpaceInvalid)
				return
			}
			page, err := s.database.SpaceTaskPage(r.Context(), userID, spaceID, db.SpaceTaskQuery{
				Status: r.URL.Query().Get("status"), AssigneeUserID: r.URL.Query().Get("assignee_user_id"), AssigneeAgentID: r.URL.Query().Get("assignee_agent_id"), Priority: r.URL.Query().Get("priority"), Search: r.URL.Query().Get("q"), DueFrom: dueFrom, DueTo: dueTo, Sort: r.URL.Query().Get("sort"), Cursor: r.URL.Query().Get("cursor"), Limit: limit, IncludeArchived: r.URL.Query().Get("include_archived") == "true",
			})
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, page)
		case http.MethodPost:
			var body struct {
				Title           string                     `json:"title"`
				Notes           string                     `json:"notes"`
				Status          string                     `json:"status"`
				Priority        string                     `json:"priority"`
				AssigneeUserID  string                     `json:"assignee_user_id"`
				AssigneeAgentID string                     `json:"assignee_agent_id"`
				DueAt           *time.Time                 `json:"due_at"`
				DueTimezone     string                     `json:"due_timezone"`
				SourceRefs      json.RawMessage            `json:"source_refs"`
				AgentRun        *db.SpaceTaskAgentRunInput `json:"agent_run"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			item, err := s.database.CreateSpaceTask(r.Context(), userID, db.SpaceTask{SpaceID: spaceID, Title: body.Title, Notes: body.Notes, Status: body.Status, Priority: body.Priority, AssigneeUserID: body.AssigneeUserID, AssigneeAgentID: body.AssigneeAgentID, DueAt: body.DueAt, DueTimezone: body.DueTimezone, SourceRefs: body.SourceRefs, AgentRun: body.AgentRun})
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			_, _ = s.ProcessSpaceTaskEvent(r.Context(), *item, "created")
			if item.AssigneeAgentID != "" {
				item.AgentRun = body.AgentRun
				s.queueAssignedPersonalAgent(r.Context(), userID, item)
			}
			writeJSON(w, http.StatusCreated, item)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (s *SpacesService) MoveSpaceTask() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body db.SpaceTaskMove
		if decodeJSON(w, r, &body) != nil {
			return
		}
		result, err := s.database.MoveSpaceTask(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "taskID"), body)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		_, _ = s.ProcessSpaceTaskEvent(r.Context(), result.Task, "moved")
		writeJSON(w, http.StatusOK, result)
	}
}

func optionalRFC3339(value string) (*time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, err
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

func (s *SpacesService) SpaceTask() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, taskID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "taskID")
		switch r.Method {
		case http.MethodPatch:
			var body db.SpaceTask
			if decodeJSON(w, r, &body) != nil {
				return
			}
			body.ID, body.SpaceID = taskID, spaceID
			item, err := s.database.UpdateSpaceTask(r.Context(), userID, body)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			_, _ = s.ProcessSpaceTaskEvent(r.Context(), *item, "updated")
			if item.AssigneeAgentID != "" {
				item.AgentRun = body.AgentRun
				s.queueAssignedPersonalAgent(r.Context(), userID, item)
			}
			writeJSON(w, http.StatusOK, item)
		case http.MethodDelete:
			version, err := strconv.ParseInt(r.URL.Query().Get("version"), 10, 64)
			if err != nil || version < 1 {
				writeSpaceError(w, db.ErrSpaceInvalid)
				return
			}
			item, err := s.database.ArchiveSpaceTask(r.Context(), userID, spaceID, taskID, version)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			_, _ = s.ProcessSpaceTaskEvent(r.Context(), *item, "archived")
			writeJSON(w, http.StatusOK, item)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (s *SpacesService) SpaceTaskActivity() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		items, err := s.database.SpaceTaskActivity(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "taskID"))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"activity": items})
	}
}

func (s *SpacesService) SpaceCalendar() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		switch r.Method {
		case http.MethodGet:
			from, fromErr := time.Parse(time.RFC3339, r.URL.Query().Get("from"))
			to, toErr := time.Parse(time.RFC3339, r.URL.Query().Get("to"))
			if fromErr != nil || toErr != nil || !to.After(from) || to.Sub(from) > 370*24*time.Hour {
				writeSpaceError(w, db.ErrSpaceInvalid)
				return
			}
			items, err := s.database.SpaceCalendarEvents(r.Context(), userID, spaceID, from, to)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"events": items})
		case http.MethodPost:
			var body db.SpaceCalendarEvent
			if decodeJSON(w, r, &body) != nil {
				return
			}
			body.SpaceID = spaceID
			item, err := s.database.CreateNativeCalendarEvent(r.Context(), userID, body)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, item)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (s *SpacesService) SpaceNativeCalendarEvent() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, eventID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "eventID")
		switch r.Method {
		case http.MethodPatch:
			var body db.SpaceCalendarEvent
			if decodeJSON(w, r, &body) != nil {
				return
			}
			body.ID, body.SpaceID = eventID, spaceID
			item, err := s.database.UpdateNativeCalendarEvent(r.Context(), userID, body)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, item)
		case http.MethodDelete:
			version, err := strconv.ParseInt(r.URL.Query().Get("version"), 10, 64)
			if err != nil {
				writeSpaceError(w, db.ErrSpaceInvalid)
				return
			}
			if err := s.database.ArchiveNativeCalendarEvent(r.Context(), userID, spaceID, eventID, version); err != nil {
				writeSpaceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

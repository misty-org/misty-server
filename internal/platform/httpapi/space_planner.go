package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/misty-org/misty-server/internal/platform/db"
)

// taskBody is a task as the app sends it. A field left out of a PATCH is left
// as it is; due_at set to null or "" clears the due date.
type taskBody struct {
	Title           *string `json:"title"`
	Notes           *string `json:"notes"`
	Status          *string `json:"status"`
	Priority        *string `json:"priority"`
	AssigneeUserID  *string `json:"assignee_user_id"`
	AssigneeAgentID *string `json:"assignee_agent_id"`
	DueAt           *string `json:"due_at"`
	ClearDue        bool    `json:"clear_due"`
}

func (body taskBody) input() (db.SpaceTaskInput, error) {
	input := db.SpaceTaskInput{
		Title: body.Title, Notes: body.Notes, Status: body.Status, Priority: body.Priority,
		AssigneeUserID: body.AssigneeUserID, AssigneeAgentID: body.AssigneeAgentID,
		ClearDue: body.ClearDue,
	}
	if body.DueAt != nil && *body.DueAt != "" {
		due, err := time.Parse(time.RFC3339, *body.DueAt)
		if err != nil {
			return input, db.ErrSpaceInvalid
		}
		input.DueAt = &due
	} else if body.DueAt != nil {
		input.ClearDue = true
	}
	return input, nil
}

// SpaceTasks lists and creates the tasks on a Space's board.
func (s *SpacesService) SpaceTasks() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		switch r.Method {
		case http.MethodGet:
			tasks, err := s.database.SpaceTasks(r.Context(), userID, spaceID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
		case http.MethodPost:
			var body taskBody
			if decodeJSON(w, r, &body) != nil {
				return
			}
			input, err := body.input()
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			task, err := s.database.CreateSpaceTask(r.Context(), userID, spaceID, input)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, task)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

// SpaceTask edits or archives one task.
func (s *SpacesService) SpaceTask() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, taskID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "taskID")
		switch r.Method {
		case http.MethodPatch:
			var body taskBody
			if decodeJSON(w, r, &body) != nil {
				return
			}
			input, err := body.input()
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			task, err := s.database.UpdateSpaceTask(r.Context(), userID, spaceID, taskID, input)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, task)
		case http.MethodDelete:
			if err := s.database.ArchiveSpaceTask(r.Context(), userID, spaceID, taskID); err != nil {
				writeSpaceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

// MoveSpaceTask puts a task in a board column, before another task or at the end.
func (s *SpacesService) MoveSpaceTask() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			Status       string `json:"status"`
			BeforeTaskID string `json:"before_task_id"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		task, reordered, err := s.database.MoveSpaceTask(
			r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "taskID"),
			body.Status, body.BeforeTaskID,
		)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"task": task, "reordered": reordered})
	}
}

type calendarEventBody struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Location    string `json:"location"`
	StartsAt    string `json:"starts_at"`
	EndsAt      string `json:"ends_at"`
	AllDay      bool   `json:"all_day"`
	Timezone    string `json:"timezone"`
}

func (body calendarEventBody) input() (db.SpaceCalendarEventInput, error) {
	startsAt, startErr := time.Parse(time.RFC3339, body.StartsAt)
	endsAt, endErr := time.Parse(time.RFC3339, body.EndsAt)
	if startErr != nil || endErr != nil {
		return db.SpaceCalendarEventInput{}, db.ErrSpaceInvalid
	}
	return db.SpaceCalendarEventInput{
		Title: body.Title, Description: body.Description, Location: body.Location,
		StartsAt: startsAt, EndsAt: endsAt, AllDay: body.AllDay, Timezone: body.Timezone,
	}, nil
}

// SpaceCalendarEvents lists the events in a time range and creates events.
func (s *SpacesService) SpaceCalendarEvents() http.HandlerFunc {
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
			if fromErr != nil || toErr != nil {
				writeSpaceError(w, db.ErrSpaceInvalid)
				return
			}
			events, err := s.database.SpaceCalendarEvents(r.Context(), userID, spaceID, from, to)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"events": events})
		case http.MethodPost:
			var body calendarEventBody
			if decodeJSON(w, r, &body) != nil {
				return
			}
			input, err := body.input()
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			event, err := s.database.CreateSpaceCalendarEvent(r.Context(), userID, spaceID, input)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, event)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

// SpaceCalendarEvent edits or deletes one event.
func (s *SpacesService) SpaceCalendarEvent() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, eventID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "eventID")
		switch r.Method {
		case http.MethodPatch:
			var body calendarEventBody
			if decodeJSON(w, r, &body) != nil {
				return
			}
			input, err := body.input()
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			event, err := s.database.UpdateSpaceCalendarEvent(r.Context(), userID, spaceID, eventID, input)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, event)
		case http.MethodDelete:
			if err := s.database.DeleteSpaceCalendarEvent(r.Context(), userID, spaceID, eventID); err != nil {
				writeSpaceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// Google Calendar-backed tasks.
//
// The product rule this file enforces: Google owns the schedule, Misty owns
// everything else, and neither silently overwrites the other. Local edits stay
// "unpublished" until someone publishes them, and a remote change to the same
// field produces a conflict the user resolves — never a lost edit.

// CreateCalendarTask creates a task bound to a Google calendar. `publish=false`
// keeps it a local draft; `true` creates the Google event straight away.
func (s *SpacesService) CreateCalendarTask() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		var body struct {
			Title            string          `json:"title"`
			Notes            string          `json:"notes"`
			Status           string          `json:"status"`
			Priority         string          `json:"priority"`
			AssigneeUserID   string          `json:"assignee_user_id"`
			CalendarSourceID string          `json:"calendar_source_id"`
			Schedule         db.TaskSchedule `json:"schedule"`
			Publish          bool            `json:"publish"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		source, err := s.calendarSource(r.Context(), userID, spaceID, body.CalendarSourceID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if err := TestingValidateTaskSchedule(&body.Schedule, source.Timezone); err != nil {
			writeSpaceError(w, err)
			return
		}
		if strings.TrimSpace(body.Title) == "" {
			body.Title = body.Schedule.Title
		}
		link := db.TaskCalendarLink{SourceID: source.ID, GoogleCalendarID: source.ExternalCalendarID}
		task, err := s.database.CreateCalendarSpaceTask(r.Context(), userID,
			db.SpaceTask{SpaceID: spaceID, Title: body.Title, Notes: body.Notes, Status: body.Status,
				Priority: body.Priority, AssigneeUserID: body.AssigneeUserID, DueTimezone: body.Schedule.Timezone,
				SourceRefs: json.RawMessage("[]")},
			body.Schedule, link)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if !body.Publish {
			writeJSON(w, http.StatusCreated, task)
			return
		}
		published, publishErr := s.publishTaskToGoogle(r.Context(), userID, spaceID, task)
		if publishErr != nil {
			// The draft survives a failed publish. Losing the user's typing
			// because Google was unreachable would be the worse outcome.
			writeJSON(w, http.StatusCreated, s.taskWithCalendarError(r.Context(), task, publishErr))
			return
		}
		writeJSON(w, http.StatusCreated, published)
	}
}

// PublishTaskToCalendar pushes a task's local schedule edits to Google.
func (s *SpacesService) PublishTaskToCalendar() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, taskID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "taskID")
		allowed, err := s.database.HasSpacePermission(r.Context(), userID, spaceID, db.PermissionTasksManage)
		if err != nil || !allowed {
			writeSpaceError(w, db.ErrSpaceForbidden)
			return
		}
		task, err := s.database.SpaceTaskByID(r.Context(), spaceID, taskID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		published, err := s.publishTaskToGoogle(r.Context(), userID, spaceID, task)
		if err != nil {
			writeProviderFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, published)
	}
}

// ResolveTaskCalendarConflict records the user's choice between their local
// edits and Google's version. Misty holds both until this is called, so neither
// side is lost to a background sync.
func (s *SpacesService) ResolveTaskCalendarConflict() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, taskID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "taskID")
		var body struct {
			Resolution string `json:"resolution"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		allowed, err := s.database.HasSpacePermission(r.Context(), userID, spaceID, db.PermissionTasksManage)
		if err != nil || !allowed {
			writeSpaceError(w, db.ErrSpaceForbidden)
			return
		}
		task, err := s.database.SpaceTaskByID(r.Context(), spaceID, taskID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		link := task.TaskCalendarLink()
		if link == nil {
			writeSpaceError(w, db.ErrSpaceInvalid)
			return
		}
		switch body.Resolution {
		case "publish_local":
			published, publishErr := s.publishTaskToGoogle(r.Context(), userID, spaceID, task)
			if publishErr != nil {
				writeProviderFailure(w, publishErr)
				return
			}
			writeJSON(w, http.StatusOK, published)
		case "discard_local":
			if link.Published == nil {
				writeSpaceError(w, db.ErrSpaceInvalid)
				return
			}
			restored := *link.Published
			updated, updateErr := s.database.SetSpaceTaskCalendar(r.Context(), spaceID, taskID, &restored, link, nil)
			if updateErr != nil {
				writeSpaceError(w, updateErr)
				return
			}
			writeJSON(w, http.StatusOK, updated)
		default:
			writeSpaceError(w, db.ErrSpaceInvalid)
		}
	}
}

// SyncCalendarTasks pulls Google changes into Misty's tasks. The underlying
// source sync uses Calendar's sync token when it has one and falls back to a
// full window otherwise; this handler then reconciles events onto tasks.
func (s *SpacesService) SyncCalendarTasks() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		var body struct {
			SourceID string `json:"source_id"`
		}
		_ = decodeJSON(w, r, &body)
		sources, err := s.database.SpaceCalendarSources(r.Context(), userID, spaceID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		tasks := []db.SpaceTask{}
		for index := range sources {
			source := sources[index]
			if source.Status == "disabled" || (body.SourceID != "" && source.ID != body.SourceID) {
				continue
			}
			if syncErr := s.syncGoogleCalendarSource(r.Context(), &source, true); syncErr != nil {
				_ = s.database.UpdateCalendarSourceSync(r.Context(), source.ID, "", "", "", "", "needs_attention", providerErrorCode(syncErr), nil)
				continue
			}
			reconciled, reconcileErr := s.reconcileCalendarTasks(r.Context(), spaceID, source)
			if reconcileErr != nil {
				continue
			}
			tasks = append(tasks, reconciled...)
		}
		refreshed, err := s.database.SpaceCalendarSources(r.Context(), userID, spaceID)
		if err != nil {
			refreshed = sources
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"tasks": tasks, "synced_at": time.Now().UTC().Format(time.RFC3339), "sources": refreshed,
		})
	}
}

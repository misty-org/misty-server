package app

import (
	"net/http"

	api "github.com/misty-org/misty-server/internal/platform/httpapi"
)

// mountPlannerRoutes registers a Space's planner: the tasks on its board and
// the events on its calendar. A person's connected calendars are read on their
// computer and never pass through here.
func (s *Server) mountPlannerRoutes(prefix string, spaces *api.SpacesService) {
	tasks := prefix + "/spaces/{spaceID}/tasks"
	s.Router.MethodFunc(http.MethodGet, tasks, spaces.SpaceTasks())
	s.Router.MethodFunc(http.MethodPost, tasks, spaces.SpaceTasks())
	s.Router.MethodFunc(http.MethodPatch, tasks+"/{taskID}", spaces.SpaceTask())
	s.Router.MethodFunc(http.MethodDelete, tasks+"/{taskID}", spaces.SpaceTask())
	s.Router.Post(tasks+"/{taskID}/move", spaces.MoveSpaceTask())

	events := prefix + "/spaces/{spaceID}/calendar/events"
	s.Router.MethodFunc(http.MethodGet, events, spaces.SpaceCalendarEvents())
	s.Router.MethodFunc(http.MethodPost, events, spaces.SpaceCalendarEvents())
	s.Router.MethodFunc(http.MethodPatch, events+"/{eventID}", spaces.SpaceCalendarEvent())
	s.Router.MethodFunc(http.MethodDelete, events+"/{eventID}", spaces.SpaceCalendarEvent())
}

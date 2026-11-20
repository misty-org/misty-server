package app

import (
	"net/http"

	api "github.com/misty-org/misty-server/internal/platform/httpapi"
)

// mountAppRoutes registers a Space's installed apps. Installing and removing are for the Space's
// mods (apps.manage); every member lists and uses them. App bundles
// never pass through the server.
func (s *Server) mountAppRoutes(prefix string, spaces *api.SpacesService) {
	apps := prefix + "/spaces/{spaceID}/apps"
	s.Router.MethodFunc(http.MethodGet, apps, spaces.SpaceApps())
	s.Router.MethodFunc(http.MethodPut, apps+"/{appID}", spaces.SpaceApp())
	s.Router.MethodFunc(http.MethodDelete, apps+"/{appID}", spaces.SpaceApp())
	// An app's live documents (the SDK's `room()`), on the collaboration relay.
	s.Router.MethodFunc(http.MethodPost, apps+"/{appID}/rooms/{roomID}/collaboration-ticket", spaces.SpaceAppRoomTicket())
}

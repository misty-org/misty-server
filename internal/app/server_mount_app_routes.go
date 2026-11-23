package app

import (
	"net/http"

	api "github.com/misty-org/misty-server/internal/platform/httpapi"
)

func (s *Server) mountAppRoutes(prefix string, spaces *api.SpacesService) {
	apps := prefix + "/spaces/{spaceID}/apps"
	s.Router.MethodFunc(http.MethodGet, apps, spaces.SpaceApps())
	s.Router.MethodFunc(http.MethodPut, apps+"/{appID}", spaces.SpaceApp())
	s.Router.MethodFunc(http.MethodDelete, apps+"/{appID}", spaces.SpaceApp())
	s.Router.MethodFunc(http.MethodPost, apps+"/{appID}/rooms/{roomID}/collaboration-ticket", spaces.SpaceAppRoomTicket())
}

package app

import (
	"net/http"

	api "github.com/misty-org/misty-server/internal/platform/httpapi"
)

// mountAppRoutes registers a Space's installed apps and the small JSON each keeps. Installing and
// removing are for the Space's mods (apps.manage); every member lists and uses them. App bundles
// never pass through the server.
func (s *Server) mountAppRoutes(prefix string, spaces *api.SpacesService) {
	apps := prefix + "/spaces/{spaceID}/apps"
	s.Router.MethodFunc(http.MethodGet, apps, spaces.SpaceApps())
	s.Router.MethodFunc(http.MethodPut, apps+"/{appID}", spaces.SpaceApp())
	s.Router.MethodFunc(http.MethodDelete, apps+"/{appID}", spaces.SpaceApp())

	storage := apps + "/{appID}/storage"
	s.Router.MethodFunc(http.MethodGet, storage, spaces.SpaceAppStorage())
	s.Router.MethodFunc(http.MethodGet, storage+"/{key}", spaces.SpaceAppStorageKey())
	s.Router.MethodFunc(http.MethodPut, storage+"/{key}", spaces.SpaceAppStorageKey())
	s.Router.MethodFunc(http.MethodDelete, storage+"/{key}", spaces.SpaceAppStorageKey())
}

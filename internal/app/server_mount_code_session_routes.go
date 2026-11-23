package app

import (
	"net/http"

	api "github.com/misty-org/misty-server/internal/platform/httpapi"
)

func (s *Server) mountCodeSessionRoutes(prefix string, spaces *api.SpacesService) {
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/code-sessions", spaces.SpaceCodeSessions())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/code-sessions", spaces.SpaceCodeSessions())
	s.Router.Get(prefix+"/spaces/{spaceID}/code-sessions/{sessionID}", spaces.SpaceCodeSession())
	s.Router.Post(prefix+"/spaces/{spaceID}/code-sessions/{sessionID}/end", spaces.EndSpaceCodeSession())
	s.Router.Post(
		prefix+"/spaces/{spaceID}/code-sessions/{sessionID}/collaboration-ticket",
		spaces.SpaceCodeSessionCollaborationTicket(),
	)
}

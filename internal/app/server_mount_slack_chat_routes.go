package app

import (
	"net/http"

	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

func (s *Server) mountSlackChatRoutes(prefix string, spaces *api.SpacesService) {
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/integrations/slack/links", spaces.SpaceSlackLinks())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/integrations/slack/links", spaces.SpaceSlackLinks())
	s.Router.MethodFunc(http.MethodPatch, prefix+"/spaces/{spaceID}/integrations/slack/links/{linkID}", spaces.SpaceSlackLink())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/integrations/slack/links/{linkID}", spaces.SpaceSlackLink())
	s.Router.Post(prefix+"/spaces/{spaceID}/integrations/slack/links/{linkID}/sync", spaces.SyncSpaceSlackLink())
	s.Router.Post(prefix+"/spaces/{spaceID}/integrations/slack/links/{linkID}/publish", spaces.PublishSpaceSlackMessage())
}

package app

import (
	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
	"net/http"
)

func (s *Server) mountFigmaRoutes(prefix string, spaces *api.SpacesService) {
	s.Router.Get(prefix+"/figma/teams/{teamID}/projects", spaces.FigmaProjects())
	s.Router.Get(prefix+"/figma/projects/{projectID}/files", spaces.FigmaProjectFiles())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/drawings/figma/bindings", spaces.FigmaBindings())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/drawings/figma/bindings", spaces.FigmaBindings())
	s.Router.Delete(prefix+"/spaces/{spaceID}/drawings/figma/bindings/{bindingID}", spaces.FigmaBinding())
	s.Router.Post(prefix+"/spaces/{spaceID}/drawings/figma/bindings/{bindingID}/sync", spaces.SyncFigmaBinding())
	s.Router.Post(prefix+"/spaces/{spaceID}/drawings/figma/bindings/{bindingID}/reconcile-webhooks", spaces.ReconcileFigmaWebhooks())
	s.Router.Get(prefix+"/spaces/{spaceID}/drawings/figma/bindings/{bindingID}/records", spaces.FigmaBindingRecords())
	s.Router.Get(prefix+"/spaces/{spaceID}/drawings/figma/bindings/{bindingID}/context", spaces.FigmaBindingContext())
	s.Router.Post(prefix+"/spaces/{spaceID}/drawings/figma/bindings/{bindingID}/comments", spaces.FigmaComments())
	s.Router.Post(prefix+"/provider-callbacks/figma", spaces.FigmaWebhook())
}

package app

import (
	"net/http"

	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

func (s *Server) mountMCPRoutes(prefix string, spaces *api.SpacesService) {
	s.Router.MethodFunc(http.MethodGet, prefix+"/mcp/connections", spaces.MCPConnections())
	s.Router.MethodFunc(http.MethodPost, prefix+"/mcp/connections", spaces.MCPConnections())
	s.Router.Get(prefix+"/mcp/connections/{connectionID}", spaces.MCPConnection())
	s.Router.Delete(prefix+"/mcp/connections/{connectionID}", spaces.MCPConnection())
	s.Router.Post(prefix+"/mcp/connections/{connectionID}/test", spaces.TestMCPConnection())
	s.Router.Post(prefix+"/mcp/connections/{connectionID}/discover", spaces.DiscoverMCPConnection())
	s.Router.Get(prefix+"/mcp/connections/{connectionID}/tools", spaces.MCPConnectionTools())
	s.Router.Post(prefix+"/mcp/oauth/activepieces/authorize", spaces.BeginActivepiecesAuthorization())
	s.Router.Get(prefix+"/oauth/mcp/activepieces/callback", spaces.ActivepiecesAuthorizationCallback())
	s.Router.Get(prefix+"/automations/flows", spaces.ActivepiecesFlows())
	s.Router.Post(prefix+"/automations/tools/{toolName}", spaces.ActivepiecesTool())
}

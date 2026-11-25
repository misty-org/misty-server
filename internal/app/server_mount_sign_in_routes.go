package app

import (
	api "github.com/misty-org/misty-server/internal/platform/httpapi"
)

func (s *Server) mountSignInRoutes(prefix string) {
	s.Router.Post(prefix+"/auth/device", api.StartDeviceSignIn(s.Database))
	s.Router.Get(prefix+"/auth/device/lookup", api.LookupDeviceSignIn(s.Database))
	s.Router.Post(prefix+"/auth/device/approve", s.liveSession(api.ApproveDeviceSignIn(s.Database)))
	s.Router.Post(prefix+"/auth/device/refuse", api.RefuseDeviceSignIn(s.Database))
	s.Router.Get(prefix+"/auth/device/{deviceID}", api.WaitDeviceSignIn(s.Database))
	s.Router.Post(prefix+"/auth/integration", api.ExchangeIntegrationToken(s.Database))
	s.Router.Get(prefix+"/me/sessions", api.ListSessions(s.Database))
	s.Router.Delete(prefix+"/me/sessions/{sessionID}", api.SignOutSession(s.Database))
}

func (s *Server) mountSpaceIntegrationRoutes(prefix string, spaces *api.SpacesService) {
	s.Router.Get(prefix+"/spaces/{spaceID}/integrations", spaces.ListSpaceIntegrations())
	s.Router.Post(prefix+"/spaces/{spaceID}/integrations", s.liveSession(spaces.CreateSpaceIntegration()))
	s.Router.Patch(prefix+"/spaces/{spaceID}/integrations/{integrationID}", spaces.UpdateSpaceIntegration())
	s.Router.Delete(prefix+"/spaces/{spaceID}/integrations/{integrationID}", spaces.RemoveSpaceIntegration())
	s.Router.Post(prefix+"/spaces/{spaceID}/integrations/{integrationID}/token", s.liveSession(spaces.ReplaceSpaceIntegrationToken()))
}

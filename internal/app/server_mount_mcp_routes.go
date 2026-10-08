package app

import (
	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

func (s *Server) mountMCPRoutes(prefix string, spaces *api.SpacesService) {
	s.Router.Get(prefix+"/integrations/apps", spaces.ConnectedApps())
	s.Router.Get(prefix+"/integrations/apps/catalog", spaces.AppCatalog())
	s.Router.Post(prefix+"/integrations/apps/connect", spaces.ConnectApp())
	s.Router.Delete(prefix+"/integrations/apps/connections/{accountID}", spaces.DisconnectApp())
	s.Router.Get(prefix+"/me/app-requests/{requestID}", spaces.AppRequestControl())
	s.Router.Post(prefix+"/me/app-requests/{requestID}", spaces.AppRequestControl())
	s.Router.Post(prefix+"/me/app-requests/{requestID}/link", spaces.AppRequestLink())
	s.Router.Post(prefix+"/me/screen-model/{jobID}", spaces.ScreenModel())
	s.Router.Post(prefix+"/me/companion/refine-point/{invocationID}", spaces.CompanionRefinePoint())
	// Collaboration: Plan/Act mode, agent questions, plans and goals.
	s.Router.Get(prefix+"/me/conversations/{conversationID}/collaboration", spaces.ConversationCollaboration())
	s.Router.Put(prefix+"/me/conversations/{conversationID}/mode", spaces.ConversationModeControl())
	s.Router.Post(prefix+"/me/conversations/{conversationID}/goal", spaces.ConversationGoal())
	s.Router.Post(prefix+"/me/agent-questions/{questionID}/answer", spaces.AnswerAgentQuestions())
	s.Router.Post(prefix+"/me/agent-plans/{planID}/{action}", spaces.AgentPlanControl())
	s.Router.Patch(prefix+"/me/agent-goals/{goalID}", spaces.AgentGoalControl())
}

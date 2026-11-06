package app

import (
	api "github.com/misty-org/misty-server/internal/platform/httpapi"
)

func (s *Server) mountAccountRoutes(prefix string, spaces *api.SpacesService) {
	s.Router.Get(prefix+"/integrations/apps", spaces.ConnectedApps())
	s.Router.Get(prefix+"/integrations/apps/catalog", spaces.AppCatalog())
	s.Router.Post(prefix+"/integrations/apps/connect", spaces.ConnectApp())
	s.Router.Delete(prefix+"/integrations/apps/connections/{accountID}", spaces.DisconnectApp())
	s.Router.Get(prefix+"/me/app-requests/{requestID}", spaces.AppRequestControl())
	s.Router.Post(prefix+"/me/app-requests/{requestID}", spaces.AppRequestControl())
	s.Router.Post(prefix+"/me/app-requests/{requestID}/link", spaces.AppRequestLink())
	// Remembered approvals: listed and revoked in Settings › Agents › Permissions.
	s.Router.Get(prefix+"/me/agent-grants", spaces.AgentGrants())
	s.Router.Delete(prefix+"/me/agent-grants", spaces.AgentGrants())
	s.Router.Delete(prefix+"/me/agent-grants/{grantID}", spaces.AgentGrant())
	s.Router.Post(prefix+"/me/agent-grants/site", spaces.AgentSiteGrant())
	s.Router.Post(prefix+"/me/voice/transcripts", spaces.VoiceTranscripts())
	s.Router.Get(prefix+"/me/voice/transcripts/{transcriptID}", spaces.VoiceTranscript())
	s.Router.Patch(prefix+"/me/voice/transcripts/{transcriptID}", spaces.VoiceTranscript())
	s.Router.Delete(prefix+"/me/voice/transcripts/{transcriptID}", spaces.VoiceTranscript())
	s.Router.Post(prefix+"/me/voice/transcripts/{transcriptID}/notes", spaces.VoiceTranscriptNotes())
	s.Router.Post(prefix+"/me/voice/transcribe", spaces.VoiceTranscribeLibraryItem())
	s.Router.Get(prefix+"/me/memory/events", spaces.MemoryEvents())
	s.Router.Post(prefix+"/me/memory/events", spaces.MemoryEvents())
	s.Router.Post(prefix+"/me/memory/forget", spaces.MemoryForget())
	s.Router.Get(prefix+"/me/memory/tree", spaces.MemoryTree())
	s.Router.Post(prefix+"/me/memory/nodes", spaces.MemoryNodeAdd())
	s.Router.Get(prefix+"/me/memory/status", spaces.MemoryStatus())
	s.Router.Get(prefix+"/me/memory/export", spaces.MemoryExport())
	s.Router.Post(prefix+"/me/memory/day-summary", spaces.MemoryDaySummary())
	// Collaboration: Plan/Act mode, agent questions, plans and goals.
	s.Router.Get(prefix+"/me/conversations/{conversationID}/collaboration", spaces.ConversationCollaboration())
	s.Router.Put(prefix+"/me/conversations/{conversationID}/mode", spaces.ConversationModeControl())
	s.Router.Post(prefix+"/me/conversations/{conversationID}/goal", spaces.ConversationGoal())
	s.Router.Post(prefix+"/me/agent-questions/{questionID}/answer", spaces.AnswerAgentQuestions())
	s.Router.Post(prefix+"/me/agent-plans/{planID}/{action}", spaces.AgentPlanControl())
	s.Router.Patch(prefix+"/me/agent-goals/{goalID}", spaces.AgentGoalControl())
}

package app

import (
	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

func (s *Server) mountMCPRoutes(prefix string, spaces *api.SpacesService) {
	s.Router.Get(prefix+"/integrations/apps", spaces.ConnectedApps())
	s.Router.Get(prefix+"/integrations/apps/catalog", spaces.AppCatalog())
	s.Router.Post(prefix+"/integrations/apps/connect", spaces.ConnectApp())
	s.Router.Delete(prefix+"/integrations/apps/connections/{accountID}", spaces.DisconnectApp())
	// Projects from and to GitHub: the code itself moves with the device's git.
	s.Router.Get(prefix+"/integrations/github/repos", spaces.GitHubRepos())
	s.Router.Post(prefix+"/integrations/github/repos", spaces.GitHubRepos())
	s.Router.Get(prefix+"/me/app-requests/{requestID}", spaces.AppRequestControl())
	s.Router.Post(prefix+"/me/app-requests/{requestID}", spaces.AppRequestControl())
	s.Router.Post(prefix+"/me/app-requests/{requestID}/link", spaces.AppRequestLink())
	// Remembered approvals: listed and revoked in Settings › Agents › Permissions.
	s.Router.Get(prefix+"/me/agent-grants", spaces.AgentGrants())
	s.Router.Delete(prefix+"/me/agent-grants", spaces.AgentGrants())
	s.Router.Delete(prefix+"/me/agent-grants/{grantID}", spaces.AgentGrant())
	s.Router.Post(prefix+"/me/agent-grants/site", spaces.AgentSiteGrant())
	s.Router.Post(prefix+"/me/agent-grants/project", spaces.AgentProjectGrants())
	// Coding through the person's own tools: the device reports its tools, takes
	// a budget lease per turn, mirrors the tool's events and asks for approvals.
	s.Router.Put(prefix+"/me/coding/engines", spaces.CodingEnginesReport())
	s.Router.Get(prefix+"/me/coding/allowance", spaces.CodingAllowance())
	s.Router.Post(prefix+"/me/coding/{invocationID}/lease", spaces.CodingLease())
	s.Router.Post(prefix+"/me/coding/{invocationID}/events", spaces.CodingEvents())
	s.Router.Post(prefix+"/me/coding/{invocationID}/approvals", spaces.CodingApproval())
	s.Router.Post(prefix+"/me/screen-model/{jobID}", spaces.ScreenModel())
	s.Router.Post(prefix+"/me/companion/refine-point/{invocationID}", spaces.CompanionRefinePoint())
	s.Router.Post(prefix+"/me/voice/transcripts", spaces.VoiceTranscripts())
	s.Router.Get(prefix+"/me/voice/transcripts/{transcriptID}", spaces.VoiceTranscript())
	s.Router.Patch(prefix+"/me/voice/transcripts/{transcriptID}", spaces.VoiceTranscript())
	s.Router.Delete(prefix+"/me/voice/transcripts/{transcriptID}", spaces.VoiceTranscript())
	s.Router.Post(prefix+"/me/voice/transcripts/{transcriptID}/notes", spaces.VoiceTranscriptNotes())
	s.Router.Post(prefix+"/me/voice/transcribe", spaces.VoiceTranscribeLibraryItem())
	s.Router.Get(prefix+"/me/memory/events", spaces.MemoryEvents())
	s.Router.Post(prefix+"/me/memory/events", spaces.MemoryEvents())
	s.Router.Post(prefix+"/me/memory/forget", spaces.MemoryForget())
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

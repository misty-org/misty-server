package app

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"

	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

func (s *Server) mountSpacesRoutes(prefix string, spaces *api.SpacesService, realtime *api.RealtimeService) {
	s.Router.MethodFunc(http.MethodGet, prefix+"/webhooks/social/instagram", spaces.InstagramSocialWebhook())
	s.Router.MethodFunc(http.MethodPost, prefix+"/webhooks/social/instagram", spaces.InstagramSocialWebhook())
	s.mountMCPRoutes(prefix, spaces)
	s.Router.Get(prefix+"/agents/{agentID}/activity", spaces.PersonalAgentActivity())
	s.Router.Get(prefix+"/agent-runs/{runID}", spaces.PersonalAgentRunDetail())
	s.Router.Post(prefix+"/agent-runs/{runID}/cancel", spaces.CancelPersonalAgentRun())
	s.Router.Post(prefix+"/agent-runs/{runID}/retry", spaces.RetryPersonalAgentRun())
	s.Router.Post(prefix+"/agent-runs/{runID}/approvals/{approvalID}", spaces.DecidePersonalAgentRunApproval())
	s.Router.Post(prefix+"/agent-runs/{runID}/contexts", spaces.AttachPersonalAgentRunContext())
	s.Router.Get(prefix+"/search/global", spaces.GlobalSearch())
	s.Router.Post(prefix+"/search/global/visual", spaces.GlobalVisualSearch())
	s.Router.Get(prefix+"/connections", spaces.ConnectedAccounts())
	s.Router.Post(prefix+"/connections/{provider}/authorize", spaces.BeginConnectedAccountAuthorization())
	s.Router.Get(prefix+"/oauth/connections/{provider}/callback", spaces.ConnectedAccountAuthorizationCallback())
	s.Router.Delete(prefix+"/connections/{connectionID}", spaces.DeleteConnectedAccount())
	s.Router.Get(prefix+"/connections/{connectionID}/social/resources", spaces.SocialResources())
	s.Router.Get(prefix+"/mail/accounts", spaces.MailAccounts())
	s.Router.Get(prefix+"/mail/folders", spaces.MailFolders())
	s.Router.Get(prefix+"/mail/threads", spaces.MailThreads())
	s.Router.Get(prefix+"/mail/threads/{threadID}", spaces.MailThread())
	s.Router.Post(prefix+"/mail/threads/{threadID}/actions", spaces.MailThreadActions())
	s.Router.Post(prefix+"/mail/drafts", spaces.MailDrafts())
	s.Router.Put(prefix+"/mail/drafts/{draftID}", spaces.MailDraft())
	s.Router.Post(prefix+"/mail/drafts/{draftID}/send", spaces.MailSendDraft())
	s.Router.Get(prefix+"/cloud/connections", spaces.CloudConnections())
	s.Router.Post(prefix+"/cloud/connections/{provider}/authorize", spaces.BeginCloudAuthorization())
	s.Router.Get(prefix+"/oauth/cloud/{provider}/callback", spaces.CloudAuthorizationCallback())
	s.Router.Post(prefix+"/cloud/connections/bind", spaces.BindConnectedAccountCloudConnection())
	s.Router.Post(prefix+"/cloud/connections/{connectionID}/handoff", spaces.CloudConnectionHandoff())
	// Kept only as an explicit, token-free deprecation response for older
	// desktop clients. Current clients redeem a one-time handoff natively.
	s.Router.Get(prefix+"/cloud/connections/{connectionID}/token", spaces.CloudConnectionToken())
	s.Router.Post(prefix+"/cloud/handoffs/redeem", spaces.RedeemCloudConnectionHandoff())
	s.Router.Delete(prefix+"/cloud/connections/{connectionID}", spaces.DeleteCloudConnection())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces", spaces.Spaces())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces", spaces.Spaces())
	s.Router.Get(prefix+"/space-templates", spaces.SpaceTemplates())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/setup", spaces.SpaceSetup())
	s.Router.MethodFunc(http.MethodPatch, prefix+"/spaces/{spaceID}/setup", spaces.SpaceSetup())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}", spaces.Space())
	s.Router.MethodFunc(http.MethodPatch, prefix+"/spaces/{spaceID}", spaces.Space())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}", spaces.Space())
	s.Router.Get(prefix+"/spaces/{spaceID}/home", api.HomeDashboard(s.Database))
	s.Router.Post(prefix+"/spaces/{spaceID}/home/visits", api.RecordHomeVisit(s.Database))
	s.Router.Get(prefix+"/spaces/{spaceID}/agenda", spaces.SpaceAgenda())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/roadmap-node-definitions", spaces.SpaceRoadmapNodeDefinitions())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/roadmap-node-definitions", spaces.SpaceRoadmapNodeDefinitions())
	s.Router.MethodFunc(http.MethodPatch, prefix+"/spaces/{spaceID}/roadmap-node-definitions/{definitionID}", spaces.SpaceRoadmapNodeDefinition())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/roadmap-node-definitions/{definitionID}", spaces.SpaceRoadmapNodeDefinition())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/roadmaps", spaces.SpaceRoadmaps())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/roadmaps", spaces.SpaceRoadmaps())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/roadmaps/{roadmapID}", spaces.SpaceRoadmap())
	s.Router.MethodFunc(http.MethodPatch, prefix+"/spaces/{spaceID}/roadmaps/{roadmapID}", spaces.SpaceRoadmap())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/roadmaps/{roadmapID}", spaces.SpaceRoadmap())
	s.Router.Post(prefix+"/spaces/{spaceID}/roadmaps/{roadmapID}/milestones", spaces.SpaceRoadmapMilestones())
	s.Router.MethodFunc(http.MethodPatch, prefix+"/spaces/{spaceID}/roadmaps/{roadmapID}/milestones/{milestoneID}", spaces.SpaceRoadmapMilestone())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/roadmaps/{roadmapID}/milestones/{milestoneID}", spaces.SpaceRoadmapMilestone())
	s.Router.Post(prefix+"/spaces/{spaceID}/roadmaps/{roadmapID}/goals", spaces.SpaceRoadmapGoals())
	s.Router.MethodFunc(http.MethodPatch, prefix+"/spaces/{spaceID}/roadmaps/{roadmapID}/goals/{goalID}", spaces.SpaceRoadmapGoal())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/roadmaps/{roadmapID}/goals/{goalID}", spaces.SpaceRoadmapGoal())
	s.Router.Put(prefix+"/spaces/{spaceID}/roadmaps/{roadmapID}/goals/{goalID}/tasks", spaces.SpaceRoadmapGoalTasks())
	s.Router.Post(prefix+"/spaces/{spaceID}/roadmaps/{roadmapID}/nodes", spaces.SpaceRoadmapNodes())
	s.Router.MethodFunc(http.MethodPatch, prefix+"/spaces/{spaceID}/roadmaps/{roadmapID}/nodes/{nodeID}", spaces.SpaceRoadmapNode())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/roadmaps/{roadmapID}/nodes/{nodeID}", spaces.SpaceRoadmapNode())
	s.Router.Post(prefix+"/spaces/{spaceID}/roadmaps/{roadmapID}/edges", spaces.SpaceRoadmapEdges())
	s.Router.MethodFunc(http.MethodPatch, prefix+"/spaces/{spaceID}/roadmaps/{roadmapID}/edges/{edgeID}", spaces.SpaceRoadmapEdges())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/roadmaps/{roadmapID}/edges/{edgeID}", spaces.SpaceRoadmapEdges())
	s.Router.Patch(prefix+"/spaces/{spaceID}/roadmaps/{roadmapID}/layout", spaces.SpaceRoadmapLayout())
	s.Router.Get(prefix+"/spaces/{spaceID}/members", spaces.Members())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/agents", spaces.SpaceAgentMemberships())
	s.Router.Get(prefix+"/spaces/{spaceID}/agents/{agentID}/toolbox", spaces.SpaceAgentToolbox())
	s.Router.Post(prefix+"/ai/runs", spaces.AIRuns())
	s.Router.Get(prefix+"/spaces/{spaceID}/agents/{agentID}/runs", spaces.AgentRunHistory())
	s.Router.Get(prefix+"/spaces/{spaceID}/members/{userID}/avatar", spaces.MemberAvatar())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/members/{userID}/permissions", spaces.MemberPermissions())
	s.Router.MethodFunc(http.MethodPut, prefix+"/spaces/{spaceID}/members/{userID}/permissions", spaces.MemberPermissions())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/invitations", spaces.Invite())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/invitations", spaces.Invite())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/invitations/{inviteID}/resend", spaces.SpaceInvitationItem())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/invitations/{inviteID}", spaces.SpaceInvitationItem())
	s.Router.Post(prefix+"/spaces/invitations/{inviteID}/accept", spaces.RespondInvite(true))
	s.Router.Post(prefix+"/spaces/invitations/{inviteID}/decline", spaces.RespondInvite(false))
	s.Router.MethodFunc(http.MethodGet, prefix+"/space-invitations/{token}", spaces.SpaceInvitationToken())
	s.Router.MethodFunc(http.MethodPost, prefix+"/space-invitations/{token}", spaces.SpaceInvitationToken())
	s.Router.Delete(prefix+"/spaces/{spaceID}/members/{userID}", spaces.RemoveMember())
	s.Router.Post(prefix+"/spaces/{spaceID}/leave", spaces.LeaveSpace())
	s.Router.Post(prefix+"/spaces/{spaceID}/transfer", spaces.TransferOwner())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/messages", spaces.Messages())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/messages", spaces.Messages())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/messages", spaces.Messages())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/conversations", spaces.Conversations())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/conversations", spaces.Conversations())
	s.Router.Get(prefix+"/spaces/{spaceID}/social/providers", spaces.SocialProviders())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/social/bindings", spaces.SocialBindings())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/social/bindings", spaces.SocialBindings())
	s.Router.Delete(prefix+"/spaces/{spaceID}/social/bindings/{bindingID}", spaces.SocialBinding())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/social/authorities", spaces.SocialSendAuthorities())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/social/authorities", spaces.SocialSendAuthorities())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/social/automation-rules", spaces.SocialAutomationRules())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/social/automation-rules", spaces.SocialAutomationRules())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/social/scheduled-messages", spaces.SocialScheduledMessages())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/social/scheduled-messages", spaces.SocialScheduledMessages())
	s.Router.Delete(prefix+"/spaces/{spaceID}/social/scheduled-messages/{scheduledID}", spaces.SocialScheduledMessage())
	s.Router.MethodFunc(http.MethodPatch, prefix+"/spaces/{spaceID}/conversations/{conversationID}", spaces.Conversation())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/conversations/{conversationID}", spaces.Conversation())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/conversations/{conversationID}/messages", spaces.ConversationMessages())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/conversations/{conversationID}/messages", spaces.ConversationMessages())
	s.Router.MethodFunc(http.MethodPut, prefix+"/spaces/{spaceID}/conversations/{conversationID}/messages/{messageID}", spaces.ConversationMessage())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/conversations/{conversationID}/messages/{messageID}", spaces.ConversationMessage())
	s.Router.MethodFunc(http.MethodPut, prefix+"/spaces/{spaceID}/conversations/{conversationID}/messages/{messageID}/reactions/{emoji}", spaces.ConversationMessageReaction())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/conversations/{conversationID}/messages/{messageID}/reactions/{emoji}", spaces.ConversationMessageReaction())
	s.Router.Post(prefix+"/spaces/{spaceID}/conversations/{conversationID}/read", spaces.MarkConversationRead())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/action-suggestion-settings", spaces.ActionSuggestionSettings())
	s.Router.MethodFunc(http.MethodPut, prefix+"/spaces/{spaceID}/action-suggestion-settings", spaces.ActionSuggestionSettings())
	s.Router.MethodFunc(http.MethodPut, prefix+"/spaces/{spaceID}/conversations/{conversationID}/action-suggestion-veto", spaces.ConversationSuggestionVeto())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/conversations/{conversationID}/action-suggestion-veto", spaces.ConversationSuggestionVeto())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/conversations/{conversationID}/action-suggestion-veto", spaces.ConversationSuggestionVeto())
	s.Router.Get(prefix+"/spaces/{spaceID}/action-suggestions", spaces.ActionSuggestions())
	s.Router.Get(prefix+"/spaces/{spaceID}/action-suggestions/{batchID}/review", spaces.ActionSuggestionReview())
	s.Router.Post(prefix+"/spaces/{spaceID}/action-suggestions/{batchID}/dismiss", spaces.DismissActionSuggestion())
	s.Router.Post(prefix+"/spaces/{spaceID}/action-suggestions/{batchID}/accept", spaces.AcceptActionSuggestion())
	s.Router.Post(prefix+"/spaces/{spaceID}/conversation-follow-ups/{followUpID}/cancel", spaces.CancelConversationFollowUp())
	s.Router.Post(prefix+"/spaces/{spaceID}/conversation-follow-ups/{followUpID}/opt-out", spaces.OptOutConversationFollowUp())
	s.Router.Post(prefix+"/spaces/{spaceID}/resources/{resourceKind}/{resourceID}/share-with-space", spaces.ShareResourceWithSpace())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/tasks", spaces.SpaceTasks())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/tasks", spaces.SpaceTasks())
	s.Router.MethodFunc(http.MethodPatch, prefix+"/spaces/{spaceID}/tasks/{taskID}", spaces.SpaceTask())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/tasks/{taskID}", spaces.SpaceTask())
	s.Router.Get(prefix+"/spaces/{spaceID}/tasks/{taskID}/activity", spaces.SpaceTaskActivity())
	s.Router.Post(prefix+"/spaces/{spaceID}/tasks/{taskID}/move", spaces.MoveSpaceTask())
	s.mountNoteRoutes(prefix, spaces)
	s.mountDrawingRoutes(prefix, spaces)
	s.mountFigmaRoutes(prefix, spaces)
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/calendar/events", spaces.SpaceCalendar())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/calendar/events", spaces.SpaceCalendar())
	s.Router.MethodFunc(http.MethodPatch, prefix+"/spaces/{spaceID}/calendar/events/{eventID}", spaces.SpaceNativeCalendarEvent())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/calendar/events/{eventID}", spaces.SpaceNativeCalendarEvent())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/calendar/sources", spaces.SpaceCalendarSources())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/calendar/sources", spaces.SpaceCalendarSources())
	s.Router.Delete(prefix+"/spaces/{spaceID}/calendar/sources/{sourceID}", spaces.SpaceCalendarSource())
	s.Router.Get(prefix+"/spaces/{spaceID}/calendar/google/calendars", spaces.AvailableGoogleCalendars())
	s.Router.Post(prefix+"/spaces/{spaceID}/calendar/sync", spaces.SyncCalendarTasks())
	s.Router.MethodFunc(http.MethodPut, prefix+"/spaces/{spaceID}/messages/{messageID}", spaces.Message())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/messages/{messageID}", spaces.Message())
	s.Router.MethodFunc(http.MethodPut, prefix+"/spaces/{spaceID}/messages/{messageID}/reactions/{emoji}", spaces.MessageReaction())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/messages/{messageID}/reactions/{emoji}", spaces.MessageReaction())
	s.Router.Post(prefix+"/spaces/{spaceID}/read", spaces.MarkRead())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/nodes", spaces.Nodes())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/nodes", spaces.Nodes())
	s.Router.MethodFunc(http.MethodPut, prefix+"/spaces/{spaceID}/nodes/{nodeID}", spaces.Node())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/nodes/{nodeID}", spaces.Node())
	s.Router.Post(prefix+"/spaces/{spaceID}/nodes/{nodeID}/resolve", spaces.ResolveTicket())
	s.Router.Get(prefix+"/spaces/resolve/{ticket}", spaces.Resolve())
	s.Router.Get(prefix+"/activity/inbox", spaces.Inbox())
	s.Router.Post(prefix+"/activity/inbox/seen", spaces.InboxSeen())
	s.Router.Post(prefix+"/activity/inbox/clear", spaces.InboxClear())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/studio/workflows", spaces.StudioResources("workflow"))
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/studio/workflows", spaces.StudioResources("workflow"))
	s.Router.Delete(prefix+"/spaces/{spaceID}/studio/workflows/{resourceID}", spaces.DeleteStudioResource("workflow"))
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/studio/workflows/{workflowID}/versions", spaces.WorkflowVersions())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/studio/workflows/{workflowID}/versions", spaces.WorkflowVersions())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/integrations", spaces.SpaceIntegrations())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/integrations/{integrationID}/resources", spaces.AvailableProviderResources())
	s.Router.MethodFunc(http.MethodPut, prefix+"/spaces/{spaceID}/integrations/{integrationID}/resources", spaces.AvailableProviderResources())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/provider-resources", spaces.ProviderSharedResources())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/provider-resources", spaces.ProviderSharedResources())
	s.Router.Delete(prefix+"/spaces/{spaceID}/provider-resources/{resourceID}", spaces.ProviderSharedResource())
	// Connections are created only through branded OAuth/install flows. The
	// legacy PUT route is intentionally not mounted because callers must never
	// supply their own credential/vault reference.
	s.Router.Post(prefix+"/spaces/{spaceID}/integrations/{provider}/authorize", spaces.BeginProviderAuthorization())
	s.Router.Get(prefix+"/oauth/providers/{provider}/callback", spaces.ProviderAuthorizationCallback())
	s.Router.Post(prefix+"/provider-callbacks/google/calendar", spaces.GoogleCalendarCallback())
	s.mountGitHubCodeRoutes(prefix, spaces)
	s.Router.Delete(prefix+"/integrations/{integrationID}", spaces.DeleteProviderIntegration())
	s.Router.Post(prefix+"/spaces/{spaceID}/integrations/{provider}/bind", spaces.BindConnectedAccountToSpaceProvider())
	s.Router.Get(prefix+"/runs/{runID}", spaces.RunDetail())
	s.Router.Post(prefix+"/runs/{runID}/approval", spaces.RunDecision())
	s.Router.Post(prefix+"/runs/{runID}/cancel", spaces.RunCancel())
	s.Router.Post(prefix+"/runs/{runID}/retry", spaces.RunRetry())
	s.Router.Post(prefix+"/realtime/tickets", realtime.Ticket())
	s.Router.Get(prefix+"/realtime", realtime.Connect())
}

func (s *Server) StartRealtime() error {
	if s.Realtime == nil {
		return nil
	}
	return s.Realtime.Start()
}

func spaceLinkEncryptionKeyFromEnv() (string, error) {
	if key := strings.TrimSpace(envconfig.Getenv("SPACE_LINK_ENCRYPTION_KEY")); key != "" {
		return key, nil
	}
	seed := strings.TrimSpace(envconfig.Getenv("DOCUMENT_SIGNING_KEY"))
	if seed == "" {
		if strings.EqualFold(strings.TrimSpace(envconfig.Getenv("MISTY_ENVIRONMENT")), "production") {
			return "", fmt.Errorf("SPACE_LINK_ENCRYPTION_KEY is required in production")
		}
		seed = "misty-development-space-link-key"
	}
	sum := sha256.Sum256([]byte("misty-space-links:" + seed))
	return base64.StdEncoding.EncodeToString(sum[:]), nil
}

func (s *Server) CleanupExpiredLibraryData(ctx context.Context, limit int) (int, error) {
	if s.Library == nil {
		return 0, nil
	}
	return s.Library.CleanupExpired(ctx, limit)
}

func (s *Server) CleanupExpiredJournalAssets(
	ctx context.Context,
	safetyWindow time.Duration,
	limit int,
) (int, error) {
	if s.Library == nil {
		return 0, nil
	}
	return s.Library.CleanupExpiredJournalAssets(ctx, safetyWindow, limit)
}

func (s *Server) mountAgentsRoutes(prefix string, service *api.AgentsService) {
	s.Router.Get(prefix+"/agents", service.PersonalAgents())
	s.Router.Get(prefix+"/agents/{agentID}", service.PersonalAgent())
	s.Router.Get(prefix+"/agents/{agentID}/avatar", service.PersonalAgentAvatar())
	s.Router.MethodFunc(http.MethodPost, prefix+"/agent-voice/transcriptions", service.AgentVoiceTranscription())
	deviceJobsEnabled := serverFeatureEnabled("MISTY_DEVICE_JOBS_ENABLED")
	connectedDevicesEnabled := serverFeatureEnabled("MISTY_CONNECTED_DEVICES_ENABLED")
	if !deviceJobsEnabled && !connectedDevicesEnabled {
		return
	}
	s.Router.Post(prefix+"/devices", service.RegisterDevice())
	s.Router.Get(prefix+"/devices", service.ListDevices())
	s.Router.Post(prefix+"/devices/{deviceID}/heartbeat", service.DeviceAuthenticated(service.HeartbeatDevice()))
	s.Router.Post(prefix+"/devices/{deviceID}/revoke", service.RevokeDevice())
	if connectedDevicesEnabled {
		s.Router.Get(prefix+"/devices/peer-ticket-keys", service.ConnectedDeviceTicketKeys())
		s.Router.Post(prefix+"/devices/{deviceID}/pairing-sessions", service.DeviceAuthenticated(service.CreateDevicePairing()))
		s.Router.Get(prefix+"/devices/{deviceID}/pairing-sessions/{sessionID}", service.DeviceAuthenticated(service.DevicePairingStatus()))
		s.Router.Post(prefix+"/devices/{deviceID}/pairing-sessions/{sessionID}/confirm", service.DeviceAuthenticated(service.ConfirmDevicePairing()))
		s.Router.Post(prefix+"/devices/{deviceID}/pairing/redeem", service.DeviceAuthenticated(service.RedeemDevicePairing()))
		s.Router.Post(prefix+"/devices/{deviceID}/presence", service.DeviceAuthenticated(service.UpdateConnectedDevicePresence()))
		s.Router.Get(prefix+"/devices/{deviceID}/peers", service.DeviceAuthenticated(service.ListConnectedDevicePeers()))
		s.Router.Put(prefix+"/devices/{deviceID}/pairs/{pairID}/name", service.DeviceAuthenticated(service.RenameConnectedDevicePeer()))
		s.Router.Put(prefix+"/devices/{deviceID}/pairs/{pairID}/clipboard-consent", service.DeviceAuthenticated(service.ConnectedDeviceClipboardConsent()))
		s.Router.Post(prefix+"/devices/{deviceID}/pairs/{pairID}/revoke", service.DeviceAuthenticated(service.RevokeConnectedDevicePair()))
		s.Router.Post(prefix+"/devices/{deviceID}/peer-tickets", service.DeviceAuthenticated(service.IssueConnectedDeviceTicket()))
	}
	if !deviceJobsEnabled {
		return
	}
	s.Router.Post(prefix+"/devices/{deviceID}/workflow-node-jobs/claim", service.DeviceAuthenticated(service.ClaimWorkflowNodeJob()))
	s.Router.Post(prefix+"/devices/{deviceID}/workflow-node-jobs/{jobID}/lease", service.DeviceAuthenticated(service.WorkflowNodeLeaseAction("renew")))
	s.Router.Post(prefix+"/devices/{deviceID}/workflow-node-jobs/{jobID}/complete", service.DeviceAuthenticated(service.WorkflowNodeLeaseAction("complete")))
	s.Router.Post(prefix+"/devices/{deviceID}/workflow-node-jobs/{jobID}/fail", service.DeviceAuthenticated(service.WorkflowNodeLeaseAction("fail")))
}

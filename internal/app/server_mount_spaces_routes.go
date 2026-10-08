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

	s.mountMCPRoutes(prefix, spaces)
	s.Router.Get(prefix+"/agent-runs/{runID}", spaces.PersonalAgentRunDetail())
	s.Router.Post(prefix+"/agent-runs/{runID}/cancel", spaces.CancelPersonalAgentRun())
	s.Router.Get(prefix+"/search/global", spaces.GlobalSearch())
	s.Router.Post(prefix+"/search/global/visual", spaces.GlobalVisualSearch())
	s.Router.Get(prefix+"/connections", spaces.ConnectedAccounts())
	s.Router.Post(prefix+"/connections/{provider}/authorize", spaces.BeginConnectedAccountAuthorization())
	s.Router.Get(prefix+"/oauth/connections/{provider}/callback", spaces.ConnectedAccountAuthorizationCallback())
	s.Router.Delete(prefix+"/connections/{connectionID}", spaces.DeleteConnectedAccount())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces", api.ConditionalGET(spaces.Spaces()))
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces", spaces.Spaces())
	s.Router.Get(prefix+"/space-templates", spaces.SpaceTemplates())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/setup", spaces.SpaceSetup())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}", spaces.Space())
	s.Router.MethodFunc(http.MethodPatch, prefix+"/spaces/{spaceID}", spaces.Space())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}", spaces.Space())
	s.Router.Get(prefix+"/spaces/{spaceID}/item-state", api.SpacePersonalItems(s.Database))
	s.Router.Patch(prefix+"/spaces/{spaceID}/item-state", api.SpacePersonalItems(s.Database))
	s.Router.Get(prefix+"/spaces/{spaceID}/agenda", api.ConditionalGET(spaces.SpaceAgenda()))
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/roadmap-node-definitions", spaces.SpaceRoadmapNodeDefinitions())
	s.Router.MethodFunc(http.MethodPatch, prefix+"/spaces/{spaceID}/roadmap-node-definitions/{definitionID}", spaces.SpaceRoadmapNodeDefinition())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/roadmap-node-definitions/{definitionID}", spaces.SpaceRoadmapNodeDefinition())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/roadmaps", api.ConditionalGET(spaces.SpaceRoadmaps()))
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
	s.Router.Get(prefix+"/spaces/{spaceID}/agent-listings", spaces.AgentListings())
	s.Router.MethodFunc(http.MethodPut, prefix+"/spaces/{spaceID}/agent-listings/{agentID}", spaces.AgentListing())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/agent-listings/{agentID}", spaces.AgentListing())
	s.Router.Get(prefix+"/agent-requests/{requestID}", spaces.AgentMemberRequest())
	s.Router.Get(prefix+"/me/agent-requests", spaces.PendingAgentMemberRequests())
	s.Router.Post(prefix+"/a2a/spaces/{spaceID}/agents/{agentID}", spaces.A2AAgent())
	s.Router.Get(prefix+"/a2a/spaces/{spaceID}/agents/{agentID}/.well-known/agent-card.json", spaces.A2AAgentCard())
	s.Router.Post(prefix+"/a2a/agents/{agentID}/token", spaces.A2AAgentToken())
	s.Router.Get(prefix+"/a2a/push", spaces.A2APushInbox())
	s.Router.Post(prefix+"/agent-requests/{requestID}/approve", spaces.DecideAgentMemberRequest(true))
	s.Router.Post(prefix+"/agent-requests/{requestID}/decline", spaces.DecideAgentMemberRequest(false))
	s.Router.Get(prefix+"/spaces/{spaceID}/members/{userID}/avatar", spaces.MemberAvatar())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/members/{userID}/permissions", spaces.MemberPermissions())
	s.Router.MethodFunc(http.MethodPut, prefix+"/spaces/{spaceID}/members/{userID}/permissions", spaces.MemberPermissions())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/invitations", spaces.Invite())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/invitations", spaces.Invite())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/invitations/{inviteID}/resend", spaces.SpaceInvitationItem())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/invitations/{inviteID}", spaces.SpaceInvitationItem())
	s.Router.MethodFunc(http.MethodGet, prefix+"/space-invitations/{token}", spaces.SpaceInvitationToken())
	s.Router.MethodFunc(http.MethodPost, prefix+"/space-invitations/{token}", spaces.SpaceInvitationToken())
	s.Router.Delete(prefix+"/spaces/{spaceID}/members/{userID}", spaces.RemoveMember())
	s.Router.Post(prefix+"/spaces/{spaceID}/leave", spaces.LeaveSpace())
	s.Router.Post(prefix+"/spaces/{spaceID}/transfer", spaces.TransferOwner())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/messages", api.ConditionalGET(spaces.Messages()))
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/messages", spaces.Messages())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/conversations", spaces.Conversations())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/conversations", spaces.Conversations())
	s.Router.MethodFunc(http.MethodPatch, prefix+"/spaces/{spaceID}/conversations/{conversationID}", spaces.Conversation())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/conversations/{conversationID}", spaces.Conversation())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/conversations/{conversationID}/messages", spaces.ConversationMessages())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/conversations/{conversationID}/messages", spaces.ConversationMessages())
	s.Router.MethodFunc(http.MethodPut, prefix+"/spaces/{spaceID}/conversations/{conversationID}/messages/{messageID}", spaces.ConversationMessage())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/conversations/{conversationID}/messages/{messageID}", spaces.ConversationMessage())
	s.Router.MethodFunc(http.MethodPut, prefix+"/spaces/{spaceID}/conversations/{conversationID}/messages/{messageID}/reactions/{emoji}", spaces.ConversationMessageReaction())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/conversations/{conversationID}/messages/{messageID}/reactions/{emoji}", spaces.ConversationMessageReaction())
	s.Router.Post(prefix+"/spaces/{spaceID}/conversations/{conversationID}/read", spaces.MarkConversationRead())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/tasks", api.ConditionalGET(spaces.SpaceTasks()))
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/tasks", spaces.SpaceTasks())
	s.Router.MethodFunc(http.MethodPatch, prefix+"/spaces/{spaceID}/tasks/{taskID}", spaces.SpaceTask())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/tasks/{taskID}", spaces.SpaceTask())
	s.Router.Post(prefix+"/spaces/{spaceID}/tasks/{taskID}/move", spaces.MoveSpaceTask())
	s.mountNoteRoutes(prefix, spaces)
	s.mountDrawingRoutes(prefix, spaces)
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
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/nodes", api.ConditionalGET(spaces.Nodes()))
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/nodes", spaces.Nodes())
	s.Router.MethodFunc(http.MethodPut, prefix+"/spaces/{spaceID}/nodes/{nodeID}", spaces.Node())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/nodes/{nodeID}", spaces.Node())
	s.Router.Post(prefix+"/spaces/{spaceID}/nodes/{nodeID}/resolve", spaces.ResolveTicket())
	s.Router.Get(prefix+"/spaces/resolve/{ticket}", spaces.Resolve())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/integrations", spaces.SpaceIntegrations())
	// Connections are created only through branded OAuth/install flows. The
	// legacy PUT route is intentionally not mounted because callers must never
	// supply their own credential/vault reference.
	s.Router.Post(prefix+"/provider-callbacks/google/calendar", spaces.GoogleCalendarCallback())
	s.Router.Post(prefix+"/spaces/{spaceID}/integrations/{provider}/bind", spaces.BindConnectedAccountToSpaceProvider())
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
	s.Router.MethodFunc(http.MethodPost, prefix+"/agent-voice/transcriptions", service.AgentVoiceTranscription())
	s.Router.Post(prefix+"/agent-voice/realtime/ticket", service.AgentVoiceRealtimeTicket())
	s.Router.Get(prefix+"/agent-voice/realtime", service.AgentVoiceRealtimeConnect())
	deviceJobsEnabled := serverFeatureEnabled("MISTY_DEVICE_JOBS_ENABLED")
	connectedDevicesEnabled := serverConnectedDevicesConfigured()
	// Devices are always available: adding one is how sync, agents and LAN
	// file sharing trust it.
	s.Router.Post(prefix+"/devices", service.RegisterDevice())
	s.Router.Get(prefix+"/devices", service.ListDevices())
	// One device identity for sync, agents and LAN file sharing. Trust changes
	// carry signatures the server verifies but cannot produce.
	s.Router.Get(prefix+"/devices/trust", service.DeviceTrust())
	s.Router.Get(prefix+"/devices/admission-requests", service.ListAdmissionRequests())
	s.Router.Get(prefix+"/devices/channel", service.DeviceChannel())
	s.Router.Post(prefix+"/devices/{deviceID}/channel-ticket", service.DeviceChannelTicket())
	s.Router.Post(prefix+"/devices/{deviceID}/heartbeat", service.DeviceAuthenticated(service.HeartbeatDevice()))
	s.Router.Post(prefix+"/devices/{deviceID}/admit", service.DeviceAuthenticated(service.AdmitDevice()))
	s.Router.Post(prefix+"/devices/{deviceID}/admission-requests", service.DeviceAuthenticated(service.CreateAdmissionRequest()))
	s.Router.Get(prefix+"/devices/{deviceID}/admission-requests/{requestID}", service.DeviceAuthenticated(service.AdmissionRequest()))
	s.Router.Post(prefix+"/devices/{deviceID}/admission-requests/{requestID}/challenge", service.DeviceAuthenticated(service.ChallengeAdmission()))
	s.Router.Post(prefix+"/devices/{deviceID}/admission-requests/{requestID}/reveal", service.DeviceAuthenticated(service.RevealAdmission()))
	s.Router.Post(prefix+"/devices/{deviceID}/admission-requests/{requestID}/approve", service.DeviceAuthenticated(service.ApproveAdmission()))
	s.Router.Post(prefix+"/devices/{deviceID}/admission-requests/{requestID}/deny", service.DeviceAuthenticated(service.DenyAdmission()))
	s.Router.Put(prefix+"/devices/{deviceID}/devices/{targetID}/name", service.DeviceAuthenticated(service.RenameDevice()))
	s.Router.Put(prefix+"/devices/{deviceID}/policy", service.DeviceAuthenticated(service.StoreDevicePolicy()))
	s.Router.Post(prefix+"/devices/{deviceID}/remove-device", service.DeviceAuthenticated(service.RemoveDevice()))
	// The cloud clipboard Worker accepts only tickets signed here; clips are
	// end-to-end encrypted and never pass through this server.
	if clipboard, err := api.ClipboardConfigFromEnv(); err == nil {
		s.Router.Post(prefix+"/devices/{deviceID}/clipboard-ticket", service.DeviceAuthenticated(service.ClipboardTicket(clipboard)))
	}
	if connectedDevicesEnabled {
		// Space peer sessions between accounts still verify server tickets.
		s.Router.Get(prefix+"/devices/peer-ticket-keys", service.ConnectedDeviceTicketKeys())
	}
	if !deviceJobsEnabled {
		return
	}
	s.Router.Post(prefix+"/devices/{deviceID}/workflow-node-jobs/claim", service.DeviceAuthenticated(service.ClaimWorkflowNodeJob()))
	s.Router.Post(prefix+"/devices/{deviceID}/workflow-node-jobs/{jobID}/begin", service.DeviceAuthenticated(service.WorkflowNodeLeaseAction("begin")))
	s.Router.Post(prefix+"/devices/{deviceID}/workflow-node-jobs/{jobID}/uncertain", service.DeviceAuthenticated(service.WorkflowNodeLeaseAction("uncertain")))
	s.Router.Post(prefix+"/devices/{deviceID}/workflow-node-jobs/{jobID}/lease", service.DeviceAuthenticated(service.WorkflowNodeLeaseAction("renew")))
	s.Router.Post(prefix+"/devices/{deviceID}/workflow-node-jobs/{jobID}/complete", service.DeviceAuthenticated(service.WorkflowNodeLeaseAction("complete")))
	s.Router.Post(prefix+"/devices/{deviceID}/workflow-node-jobs/{jobID}/fail", service.DeviceAuthenticated(service.WorkflowNodeLeaseAction("fail")))
}

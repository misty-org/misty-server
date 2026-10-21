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
	s.Router.Get(prefix+"/connections", spaces.ConnectedAccounts())
	s.Router.Post(prefix+"/connections/{provider}/authorize", spaces.BeginConnectedAccountAuthorization())
	s.Router.Get(prefix+"/oauth/connections/{provider}/callback", spaces.ConnectedAccountAuthorizationCallback())
	s.Router.Delete(prefix+"/connections/{connectionID}", spaces.DeleteConnectedAccount())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces", api.ConditionalGET(spaces.Spaces()))
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces", spaces.Spaces())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}", spaces.Space())
	s.Router.MethodFunc(http.MethodPatch, prefix+"/spaces/{spaceID}", spaces.Space())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}", spaces.Space())
	s.Router.Get(prefix+"/spaces/{spaceID}/item-state", api.SpacePersonalItems(s.Database))
	s.Router.Patch(prefix+"/spaces/{spaceID}/item-state", api.SpacePersonalItems(s.Database))
	s.Router.Get(prefix+"/spaces/{spaceID}/members", spaces.Members())
	s.Router.Get(prefix+"/spaces/{spaceID}/agents", spaces.SpaceAgents())
	s.Router.Get(prefix+"/me/space-agents", spaces.MySpaceAgents())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/code-projects", spaces.SpaceCodeProjects())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/code-projects", spaces.SpaceCodeProjects())
	s.Router.Delete(prefix+"/spaces/{spaceID}/code-projects/{projectID}", spaces.SpaceCodeProject())
	s.Router.Get(prefix+"/spaces/{spaceID}/agent-artifacts", spaces.AgentArtifacts())
	s.Router.MethodFunc(http.MethodPut, prefix+"/spaces/{spaceID}/agents/{agentID}", spaces.SpaceAgent())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/agents/{agentID}", spaces.SpaceAgent())
	s.Router.Get(prefix+"/agent-mentions", spaces.AgentMentions())
	s.Router.Get(prefix+"/agent-requests/{requestID}", spaces.AgentMemberRequest())
	s.Router.Get(prefix+"/agent-requests/{requestID}/transcript", spaces.AgentRequestTranscript())
	s.Router.Post(prefix+"/a2a/spaces/{spaceID}/agents/{agentID}", spaces.A2AAgent())
	s.Router.Get(prefix+"/a2a/spaces/{spaceID}/agents/{agentID}/.well-known/agent-card.json", spaces.A2AAgentCard())
	s.Router.Post(prefix+"/a2a/agents/{agentID}/token", spaces.A2AAgentToken())
	s.Router.Get(prefix+"/a2a/push", spaces.A2APushInbox())
	s.Router.Get(prefix+"/spaces/{spaceID}/members/{userID}/avatar", spaces.MemberAvatar())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/invitations", spaces.Invite())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/invitations", spaces.Invite())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/invitations/{inviteID}/resend", spaces.SpaceInvitationItem())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/invitations/{inviteID}", spaces.SpaceInvitationItem())
	s.Router.MethodFunc(http.MethodGet, prefix+"/space-invitations/{token}", spaces.SpaceInvitationToken())
	s.Router.MethodFunc(http.MethodPost, prefix+"/space-invitations/{token}", spaces.SpaceInvitationToken())
	s.Router.Delete(prefix+"/spaces/{spaceID}/members/{userID}", spaces.RemoveMember())
	s.Router.Post(prefix+"/spaces/{spaceID}/leave", spaces.LeaveSpace())
	s.Router.Put(prefix+"/spaces/{spaceID}/items/{kind}/{itemID}/visibility", spaces.SpaceItemVisibility())
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
	s.mountNoteRoutes(prefix, spaces)
	s.mountDrawingRoutes(prefix, spaces)
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
	s.Router.Post(prefix+"/me/voice/live/ticket", service.VoiceLiveTicket())
	deviceJobsEnabled := serverFeatureEnabled("MISTY_DEVICE_JOBS_ENABLED")
	connectedDevicesEnabled := serverConnectedDevicesConfigured()
	// Devices are always available: adding one is how sync, agents and the
	// shared clipboard trust it.
	s.Router.Post(prefix+"/devices", service.RegisterDevice())
	s.Router.Get(prefix+"/devices", service.ListDevices())
	// One device identity for sync, agents and the shared clipboard. Trust changes
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
	s.Router.Post(prefix+"/devices/{deviceID}/workflow-node-jobs/{jobID}/release", service.DeviceAuthenticated(service.WorkflowNodeLeaseAction("release")))
}

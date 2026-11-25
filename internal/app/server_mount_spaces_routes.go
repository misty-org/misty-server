package app

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/misty-org/misty-server/internal/platform/config"

	api "github.com/misty-org/misty-server/internal/platform/httpapi"
)

func (s *Server) mountSpacesRoutes(prefix string, spaces *api.SpacesService, realtime *api.RealtimeService) {
	s.mountAccountRoutes(prefix, spaces)
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces", api.ConditionalGET(spaces.Spaces()))
	s.Router.Get(prefix+"/search", spaces.Search())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces", spaces.Spaces())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}", spaces.Space())
	s.Router.MethodFunc(http.MethodPatch, prefix+"/spaces/{spaceID}", spaces.Space())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}", spaces.Space())
	s.Router.Get(prefix+"/spaces/{spaceID}/item-state", api.SpacePersonalItems(s.Database))
	s.Router.Patch(prefix+"/spaces/{spaceID}/item-state", api.SpacePersonalItems(s.Database))
	s.Router.Get(prefix+"/spaces/{spaceID}/members", spaces.Members())
	s.Router.Put(prefix+"/spaces/{spaceID}/members/{userID}/roles", spaces.MemberRoles())
	s.mountSpaceIntegrationRoutes(prefix, spaces)
	s.Router.Get(prefix+"/spaces/{spaceID}/roles", spaces.Roles())
	s.Router.Post(prefix+"/spaces/{spaceID}/roles", spaces.Roles())
	s.Router.Put(prefix+"/spaces/{spaceID}/roles/order", spaces.RoleOrder())
	s.Router.Patch(prefix+"/spaces/{spaceID}/roles/{roleID}", spaces.Role())
	s.Router.Delete(prefix+"/spaces/{spaceID}/roles/{roleID}", spaces.Role())
	s.Router.Get(prefix+"/spaces/{spaceID}/boosts", spaces.SpaceBoosts())
	s.Router.Get(prefix+"/spaces/{spaceID}/agents", spaces.SpaceAgents())
	s.Router.Put(prefix+"/spaces/{spaceID}/agents/{agentID}", spaces.SpaceAgent())
	s.Router.Delete(prefix+"/spaces/{spaceID}/agents/{agentID}", spaces.SpaceAgent())
	s.Router.Get(prefix+"/me/space-agents", spaces.MySpaceAgents())
	s.Router.Get(prefix+"/spaces/{spaceID}/agents/{agentID}/threads", spaces.AgentThreads())
	s.Router.Get(prefix+"/spaces/{spaceID}/agent-threads", spaces.AgentThreads())
	s.Router.Put(prefix+"/spaces/{spaceID}/agent-threads/{threadID}", spaces.AgentThread())
	s.Router.Delete(prefix+"/spaces/{spaceID}/agent-threads/{threadID}", spaces.AgentThread())
	s.Router.Get(prefix+"/spaces/{spaceID}/agent-threads/{threadID}/entries", spaces.AgentThreadEntries())
	s.Router.Post(prefix+"/spaces/{spaceID}/agent-threads/{threadID}/entries", spaces.AgentThreadEntries())
	s.Router.Post(prefix+"/spaces/{spaceID}/agent-threads/{threadID}/reactions", spaces.AgentThreadReaction())
	s.Router.Get(prefix+"/spaces/{spaceID}/members/{userID}/avatar", spaces.MemberAvatar())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/avatar", spaces.SpaceAvatar())
	s.Router.MethodFunc(http.MethodPut, prefix+"/spaces/{spaceID}/avatar", spaces.SpaceAvatar())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/avatar", spaces.SpaceAvatar())
	s.Router.Put(prefix+"/spaces/{spaceID}/avatar/decoration", spaces.SpaceAvatarDecoration())
	s.Router.Post(prefix+"/spaces/invitations/{inviteID}/{action}", spaces.SpaceInvitationResponse())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/invitations", spaces.Invite())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/invitations", spaces.Invite())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/invitations/{inviteID}/resend", spaces.SpaceInvitationItem())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/invitations/{inviteID}", spaces.SpaceInvitationItem())
	s.Router.MethodFunc(http.MethodGet, prefix+"/space-invitations/{token}", spaces.SpaceInvitationToken())
	s.Router.MethodFunc(http.MethodPost, prefix+"/space-invitations/{token}", spaces.SpaceInvitationToken())
	s.Router.Delete(prefix+"/spaces/{spaceID}/members/{userID}", spaces.RemoveMember())
	s.Router.Post(prefix+"/spaces/{spaceID}/leave", spaces.LeaveSpace())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/agent-activity", spaces.AgentActivity())
	s.Router.Put(prefix+"/spaces/{spaceID}/items/{kind}/{itemID}/visibility", spaces.SpaceItemVisibility())
	s.Router.Post(prefix+"/spaces/{spaceID}/transfer", spaces.TransferOwner())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/messages", api.ConditionalGET(spaces.Messages()))
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/messages", spaces.Messages())
	s.Router.Get(prefix+"/spaces/{spaceID}/messages/search", spaces.SearchMessages())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/conversations", spaces.Conversations())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/conversations", spaces.Conversations())
	s.Router.Post(prefix+"/spaces/{spaceID}/conversations/dm", api.Gone("dms_moved"))
	s.Router.MethodFunc(http.MethodPatch, prefix+"/spaces/{spaceID}/conversations/{conversationID}", spaces.Conversation())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/conversations/{conversationID}", spaces.Conversation())
	s.Router.Put(prefix+"/spaces/{spaceID}/conversations/{conversationID}/overwrites", spaces.ConversationOverwrites())
	s.Router.Put(prefix+"/spaces/{spaceID}/conversations/{conversationID}/place", spaces.ConversationPlace())
	s.Router.Post(prefix+"/spaces/{spaceID}/categories", spaces.ChannelCategories())
	s.Router.Patch(prefix+"/spaces/{spaceID}/categories/{categoryID}", spaces.ChannelCategory())
	s.Router.Delete(prefix+"/spaces/{spaceID}/categories/{categoryID}", spaces.ChannelCategory())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/conversations/{conversationID}/messages", spaces.ConversationMessages())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/conversations/{conversationID}/messages", spaces.ConversationMessages())
	s.Router.MethodFunc(http.MethodPut, prefix+"/spaces/{spaceID}/conversations/{conversationID}/messages/{messageID}", spaces.ConversationMessage())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/conversations/{conversationID}/messages/{messageID}", spaces.ConversationMessage())
	s.Router.MethodFunc(http.MethodPut, prefix+"/spaces/{spaceID}/conversations/{conversationID}/messages/{messageID}/reactions/{emoji}", spaces.ConversationMessageReaction())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/conversations/{conversationID}/messages/{messageID}/reactions/{emoji}", spaces.ConversationMessageReaction())
	s.Router.Post(prefix+"/spaces/{spaceID}/conversations/{conversationID}/read", spaces.MarkConversationRead())
	s.Router.Post(prefix+"/spaces/{spaceID}/conversations/{conversationID}/members", spaces.ConversationMembers())
	s.Router.Delete(prefix+"/spaces/{spaceID}/conversations/{conversationID}/members/me", spaces.LeaveConversation())
	s.Router.MethodFunc(http.MethodPut, prefix+"/spaces/{spaceID}/conversations/{conversationID}/agents/{agentID}", spaces.ConversationAgent())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/conversations/{conversationID}/agents/{agentID}", spaces.ConversationAgent())
	s.Router.Get(prefix+"/spaces/{spaceID}/threads", spaces.Threads())
	s.Router.Get(prefix+"/spaces/{spaceID}/messages/{messageID}/replies", spaces.ThreadReplies())
	s.Router.Post(prefix+"/spaces/{spaceID}/messages/{messageID}/thread/read", spaces.ThreadRead())
	s.mountNoteRoutes(prefix, spaces)
	s.Router.MethodFunc(http.MethodPut, prefix+"/spaces/{spaceID}/messages/{messageID}", spaces.Message())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/messages/{messageID}", spaces.Message())
	s.Router.MethodFunc(http.MethodPut, prefix+"/spaces/{spaceID}/messages/{messageID}/reactions/{emoji}", spaces.MessageReaction())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/messages/{messageID}/reactions/{emoji}", spaces.MessageReaction())
	s.Router.Post(prefix+"/spaces/{spaceID}/read", spaces.MarkRead())
	s.Router.Put(prefix+"/spaces/{spaceID}/messages/{messageID}/pin", spaces.MessagePin())
	s.Router.Delete(prefix+"/spaces/{spaceID}/messages/{messageID}/pin", spaces.MessagePin())
	s.Router.Get(prefix+"/spaces/{spaceID}/pins", spaces.Pins())
	s.Router.Put(prefix+"/spaces/{spaceID}/conversations/{conversationID}/messages/{messageID}/pin", spaces.MessagePin())
	s.Router.Delete(prefix+"/spaces/{spaceID}/conversations/{conversationID}/messages/{messageID}/pin", spaces.MessagePin())
	s.Router.Get(prefix+"/spaces/{spaceID}/conversations/{conversationID}/pins", spaces.Pins())
	s.mountSocialRoutes(prefix, api.NewSocialService(s.Database, realtime))
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/nodes", api.ConditionalGET(spaces.Nodes()))
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/nodes/{nodeID}", spaces.Node())
	s.Router.Post(prefix+"/spaces/{spaceID}/nodes/{nodeID}/resolve", spaces.ResolveTicket())
	s.Router.Get(prefix+"/spaces/resolve/{ticket}", spaces.Resolve())
	s.Router.Post(prefix+"/realtime/tickets", s.liveSession(realtime.Ticket()))
	s.Router.Get(prefix+"/realtime", realtime.Connect())
}

func (s *Server) StartRealtime() error {
	if s.Realtime == nil {
		return nil
	}
	return s.Realtime.Start()
}

func spaceLinkEncryptionKeyFromEnv() (string, error) {
	if key := strings.TrimSpace(config.Getenv("SPACE_LINK_ENCRYPTION_KEY")); key != "" {
		return key, nil
	}
	seed := strings.TrimSpace(config.Getenv("DOCUMENT_SIGNING_KEY"))
	if seed == "" {
		if strings.EqualFold(strings.TrimSpace(config.Getenv("MISTY_ENVIRONMENT")), "production") {
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

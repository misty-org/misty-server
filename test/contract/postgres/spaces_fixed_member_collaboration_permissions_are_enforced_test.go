package db

import (
	"context"
	"errors"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestFixedMemberCollaborationPermissionsAreEnforced(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()

	owner, err := database.CreateUser("Chat Owner", "chat-permission-owner@example.com", "password123")
	if err != nil {
		t.Fatalf("CreateUser(owner) error = %v", err)
	}
	member, err := database.CreateUser("Chat Member", "chat-permission-member@example.com", "password123")
	if err != nil {
		t.Fatalf("CreateUser(member) error = %v", err)
	}
	outsider, err := database.CreateUser("Chat Outsider", "chat-permission-outsider@example.com", "password123")
	if err != nil {
		t.Fatalf("CreateUser(outsider) error = %v", err)
	}
	space := createTestSpace(t, database, ctx, owner.ID, "Chat")
	invite, err := database.InviteToSpace(ctx, owner.ID, space.ID, member.Email)
	if err != nil {
		t.Fatalf("InviteToSpace() error = %v", err)
	}
	if _, err := database.RespondToSpaceInvite(ctx, member.ID, invite.ID, true); err != nil {
		t.Fatalf("RespondToSpaceInvite() error = %v", err)
	}

	memberSpace, err := database.SpaceByID(ctx, member.ID, space.ID)
	if err != nil || !memberSpace.Permissions[PermissionMessagesRead] || !memberSpace.Permissions[PermissionMessagesWrite] {
		t.Fatalf("default member chat permissions = %#v, %v, want read and write", memberSpace, err)
	}
	for _, permission := range []string{
		PermissionAttachmentUpload,
		PermissionLibraryView,
		PermissionLibraryUpload,
		PermissionLibraryAdd,
		PermissionLibraryEdit,
		PermissionLibraryDownload,
		PermissionLibraryImport,
		PermissionTasksView,
		PermissionTasksManage,
	} {
		if !memberSpace.Permissions[permission] {
			t.Fatalf("member permission %q = false, want fixed collaboration access", permission)
		}
	}
	for _, permission := range []string{
		PermissionIntegrationsManage,
		PermissionStorageManage,
		PermissionStorageViewMembers,
		PermissionStudioManage,
	} {
		if memberSpace.Permissions[permission] {
			t.Fatalf("member permission %q = true, want owner-only administration", permission)
		}
	}

	memberMessage, _, err := database.CreateSpaceMessage(
		ctx,
		member.ID,
		space.ID,
		[]MessageSpan{{Type: "text", Text: "Member can post by default"}},
		nil,
	)
	if err != nil {
		t.Fatalf("CreateSpaceMessage(member) error = %v", err)
	}
	if _, err := database.CreateSpaceTask(ctx, member.ID, SpaceTask{
		SpaceID: space.ID,
		Title:   "Member-created task",
		Status:  "todo",
	}); err != nil {
		t.Fatalf("CreateSpaceTask(member) error = %v", err)
	}
	if _, _, err := database.CreateSpaceMessage(
		ctx,
		outsider.ID,
		space.ID,
		[]MessageSpan{{Type: "text", Text: "Not a member"}},
		nil,
	); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("CreateSpaceMessage(outsider) error = %v, want ErrSpaceForbidden", err)
	}
	if _, err := database.InviteToSpace(ctx, member.ID, space.ID, outsider.Email); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("InviteToSpace(member) error = %v, want ErrSpaceForbidden", err)
	}
	if err := database.RemoveSpaceMember(ctx, member.ID, space.ID, owner.ID); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("RemoveSpaceMember(member) error = %v, want ErrSpaceForbidden", err)
	}
	if err := database.DeleteSpace(ctx, member.ID, space.ID, space.Name); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("DeleteSpace(member) error = %v, want ErrSpaceForbidden", err)
	}

	ownerMessage, _, err := database.CreateSpaceMessage(
		ctx,
		owner.ID,
		space.ID,
		[]MessageSpan{{Type: "text", Text: "Visible to every Space member"}},
		nil,
	)
	if err != nil {
		t.Fatalf("CreateSpaceMessage(owner) error = %v", err)
	}
	if inbox, err := database.SpaceInbox(ctx, member.ID, "unreads", 20); err != nil || !containsInboxMessage(inbox, ownerMessage.ID) {
		t.Fatalf("SpaceInbox(member) = %#v, %v, want message %q", inbox, err, ownerMessage.ID)
	}
	events, _, err := database.SpaceEventsAfter(ctx, member.ID, 0, 500)
	if err != nil {
		t.Fatalf("SpaceEventsAfter(member) error = %v", err)
	}
	if !containsSpaceEvent(events, "message.created", memberMessage.ID) ||
		!containsSpaceEvent(events, "message.created", ownerMessage.ID) {
		t.Fatalf("member events = %#v, want both message events", events)
	}
}

func TestSpaceGroupConversationsStayScopedToSelectedMembers(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Group Owner", "group-owner@example.com", "password123")
	if err != nil {
		t.Fatalf("CreateUser(owner) error = %v", err)
	}
	member, err := database.CreateUser("Included Member", "group-member@example.com", "password123")
	if err != nil {
		t.Fatalf("CreateUser(member) error = %v", err)
	}
	excluded, err := database.CreateUser("Excluded Member", "group-excluded@example.com", "password123")
	if err != nil {
		t.Fatalf("CreateUser(excluded) error = %v", err)
	}
	space := createTestSpace(t, database, ctx, owner.ID, "Group conversations")
	for _, invited := range []*User{member, excluded} {
		invite, inviteErr := database.InviteToSpace(ctx, owner.ID, space.ID, invited.Email)
		if inviteErr != nil {
			t.Fatalf("InviteToSpace(%s) error = %v", invited.Email, inviteErr)
		}
		if _, inviteErr = database.RespondToSpaceInvite(ctx, invited.ID, invite.ID, true); inviteErr != nil {
			t.Fatalf("RespondToSpaceInvite(%s) error = %v", invited.Email, inviteErr)
		}
	}

	conversation, err := database.CreateSpaceConversation(ctx, owner.ID, space.ID, "Launch crew", []SpaceActorRef{{Kind: "person", UserID: member.ID}})
	if err != nil {
		t.Fatalf("CreateSpaceConversation() error = %v", err)
	}
	if len(conversation.Participants) != 2 {
		t.Fatalf("conversation participants = %#v, want creator and selected member", conversation.Participants)
	}
	memberConversations, err := database.SpaceConversations(ctx, member.ID, space.ID)
	if err != nil || len(memberConversations) != 1 || memberConversations[0].ID != conversation.ID {
		t.Fatalf("SpaceConversations(included) = %#v, %v", memberConversations, err)
	}
	excludedConversations, err := database.SpaceConversations(ctx, excluded.ID, space.ID)
	if err != nil || len(excludedConversations) != 0 {
		t.Fatalf("SpaceConversations(excluded) = %#v, %v, want none", excludedConversations, err)
	}

	message, _, err := database.CreateSpaceConversationMessageWithReferences(ctx, member.ID, space.ID, conversation.ID, []MessageSpan{{Type: "text", Text: "Private launch note"}}, nil, nil, nil, "")
	if err != nil {
		t.Fatalf("CreateSpaceConversationMessageWithReferences() error = %v", err)
	}
	if message.ConversationID != conversation.ID {
		t.Fatalf("message conversation = %q, want %q", message.ConversationID, conversation.ID)
	}
	ownerEvents, _, err := database.SpaceEventsAfter(ctx, owner.ID, 0, 500)
	if err != nil {
		t.Fatalf("SpaceEventsAfter(owner) error = %v", err)
	}
	var groupMessageEventID, groupConversationEventID int64
	for _, event := range ownerEvents {
		if event.EventType == "message.created" && event.EntityID == message.ID {
			groupMessageEventID = event.ID
		}
		if event.EventType == "conversation.created" && event.EntityID == conversation.ID {
			groupConversationEventID = event.ID
		}
	}
	if groupMessageEventID == 0 || groupConversationEventID == 0 {
		t.Fatalf("owner events = %#v, want private conversation and message events", ownerEvents)
	}
	excludedEvents, _, err := database.SpaceEventsAfter(ctx, excluded.ID, 0, 500)
	if err != nil {
		t.Fatalf("SpaceEventsAfter(excluded) error = %v", err)
	}
	for _, event := range excludedEvents {
		if event.ID == groupMessageEventID || event.ID == groupConversationEventID {
			t.Fatalf("excluded member received private conversation event %#v", event)
		}
	}
	if event, eventErr := database.EventByIDForUser(ctx, excluded.ID, groupMessageEventID); !errors.Is(eventErr, ErrSpaceNotFound) || event != nil {
		t.Fatalf("EventByIDForUser(excluded) = %#v, %v, want ErrSpaceNotFound", event, eventErr)
	}
	if event, eventErr := database.EventByIDForUser(ctx, excluded.ID, groupConversationEventID); !errors.Is(eventErr, ErrSpaceNotFound) || event != nil {
		t.Fatalf("EventByIDForUser(excluded conversation) = %#v, %v, want ErrSpaceNotFound", event, eventErr)
	}
	groupMessages, err := database.SpaceConversationMessages(ctx, owner.ID, space.ID, conversation.ID, 0, 20)
	if err != nil || len(groupMessages) != 1 || groupMessages[0].ID != message.ID {
		t.Fatalf("SpaceConversationMessages(owner) = %#v, %v", groupMessages, err)
	}
	if _, err := database.SpaceConversationMessages(ctx, excluded.ID, space.ID, conversation.ID, 0, 20); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("SpaceConversationMessages(excluded) error = %v, want ErrSpaceForbidden", err)
	}
	selectedGroup, err := database.IsSpaceConversationForMember(ctx, member.ID, space.ID, conversation.ID)
	if err != nil || !selectedGroup {
		t.Fatalf("IsSpaceConversationForMember(included) = %v, %v, want true", selectedGroup, err)
	}
	selectedGroup, err = database.IsSpaceConversationForMember(ctx, member.ID, space.ID, message.ID)
	if err != nil || selectedGroup {
		t.Fatalf("IsSpaceConversationForMember(everyone message) = %v, %v, want false", selectedGroup, err)
	}
	if selectedGroup, err = database.IsSpaceConversationForMember(ctx, excluded.ID, space.ID, conversation.ID); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("IsSpaceConversationForMember(excluded) = %v, %v, want ErrSpaceForbidden", selectedGroup, err)
	}
	if _, _, err := database.CreateSpaceConversationMessageWithReferences(ctx, excluded.ID, space.ID, conversation.ID, []MessageSpan{{Type: "text", Text: "Not allowed"}}, nil, nil, nil, ""); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("CreateSpaceConversationMessageWithReferences(excluded) error = %v, want ErrSpaceForbidden", err)
	}
	if _, _, err := database.CreateSpaceConversationMessageWithReferences(ctx, owner.ID, space.ID, conversation.ID, []MessageSpan{{Type: "mention", UserID: excluded.ID, Label: excluded.Name}}, nil, nil, nil, ""); !errors.Is(err, ErrSpaceInvalid) {
		t.Fatalf("mention excluded member error = %v, want ErrSpaceInvalid", err)
	}
	defaultMessages, err := database.SpaceMessages(ctx, owner.ID, space.ID, 0, 20)
	if err != nil || len(defaultMessages) != 0 {
		t.Fatalf("SpaceMessages(default) = %#v, %v, want group message isolation", defaultMessages, err)
	}
}

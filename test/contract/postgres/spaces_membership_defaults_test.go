package db

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestAccountsStartWithMistyAndStandardSpacesBecomeSharedOnlyByInvite(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()

	owner, err := database.CreateUser("Owner", "space-owner@example.com", "password123")
	if err != nil {
		t.Fatalf("CreateUser(owner) error = %v", err)
	}
	member, err := database.CreateUser("Member", "space-member@example.com", "password123")
	if err != nil {
		t.Fatalf("CreateUser(member) error = %v", err)
	}

	ownerSpaces, err := database.ListSpaces(ctx, owner.ID)
	if err != nil {
		t.Fatalf("ListSpaces(owner) error = %v", err)
	}
	if len(ownerSpaces) != 1 || ownerSpaces[0].Kind != "misty" || ownerSpaces[0].Name != "Misty" {
		t.Fatalf("initial owner Spaces = %#v, want canonical Misty Space", ownerSpaces)
	}
	project, err := database.CreateSpace(ctx, owner.ID, "Project")
	if err != nil {
		t.Fatalf("CreateSpace(Project) error = %v", err)
	}
	renamed, err := database.RenameSpace(ctx, owner.ID, project.ID, "Home base")
	if err != nil || renamed.Name != "Home base" {
		t.Fatalf("RenameSpace() = %#v, %v, want renamed Space", renamed, err)
	}
	secondAdditional, err := database.CreateSpace(ctx, owner.ID, "Another")
	if err != nil {
		t.Fatalf("CreateSpace(second additional) = %#v, %v, want success", secondAdditional, err)
	}
	thirdAdditional, err := database.CreateSpace(ctx, owner.ID, "Third additional")
	if err != nil {
		t.Fatalf("CreateSpace(third additional) = %#v, %v, want success", thirdAdditional, err)
	}
	if _, err := database.CreateSpace(ctx, owner.ID, "Fourth collaborative Space"); !errors.Is(err, ErrSpaceLimit) {
		t.Fatalf("CreateSpace(fourth collaborative) error = %v, want ErrSpaceLimit", err)
	}
	if err := database.DeleteSpace(ctx, owner.ID, secondAdditional.ID, secondAdditional.Name); err != nil {
		t.Fatalf("DeleteSpace(second additional) error = %v", err)
	}
	if _, err := database.CreateSpace(ctx, owner.ID, "Still another Space"); !errors.Is(err, ErrSpaceLimit) {
		t.Fatalf("CreateSpace while deletion pending error = %v, want ErrSpaceLimit because inactive memberships still count", err)
	}

	memberSpaces, err := database.ListSpaces(ctx, member.ID)
	if err != nil || len(memberSpaces) != 1 || memberSpaces[0].Kind != "misty" || memberSpaces[0].Name != "Misty" {
		t.Fatalf("ListSpaces(member) = %#v, %v, want canonical Misty Space", memberSpaces, err)
	}

	invite, err := database.InviteToSpace(ctx, owner.ID, project.ID, member.Email)
	if err != nil {
		t.Fatalf("InviteToSpace() error = %v", err)
	}
	projectAfterInvite, err := database.SpaceByID(ctx, owner.ID, project.ID)
	if err != nil || !projectAfterInvite.IsShared || projectAfterInvite.PendingCount != 1 {
		t.Fatalf("Space after invite = %#v, %v, want shared with one pending", projectAfterInvite, err)
	}

	if _, err := database.RespondToSpaceInvite(ctx, member.ID, invite.ID, true); err != nil {
		t.Fatalf("RespondToSpaceInvite() error = %v", err)
	}
	projectAfterAccept, err := database.SpaceByID(ctx, owner.ID, project.ID)
	if err != nil || !projectAfterAccept.IsShared || projectAfterAccept.MemberCount != 2 || projectAfterAccept.PendingCount != 0 {
		t.Fatalf("Space after accept = %#v, %v, want two active members", projectAfterAccept, err)
	}
	message, _, err := database.CreateSpaceMessage(ctx, owner.ID, project.ID, []MessageSpan{{Type: "text", Text: "Shared hello"}}, nil)
	if err != nil {
		t.Fatalf("CreateSpaceMessage(shared Personal) error = %v", err)
	}
	if message.SenderUserID != owner.ID {
		t.Fatalf("shared message sender = %q, want %q", message.SenderUserID, owner.ID)
	}
	edited, err := database.UpdateSpaceMessage(ctx, owner.ID, project.ID, message.ID, []MessageSpan{{Type: "text", Text: "Edited hello"}}, nil)
	if err != nil || edited.EditedAt == nil || len(edited.Content) != 1 || edited.Content[0].Text != "Edited hello" {
		t.Fatalf("UpdateSpaceMessage(owner) = %#v, %v, want edited message", edited, err)
	}
	if _, err := database.UpdateSpaceMessage(ctx, member.ID, project.ID, message.ID, []MessageSpan{{Type: "text", Text: "Not mine"}}, nil); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("UpdateSpaceMessage(member) error = %v, want ErrSpaceForbidden", err)
	}
	inbox, err := database.SpaceInbox(ctx, member.ID, "unreads", 20)
	if err != nil {
		t.Fatalf("SpaceInbox(member) error = %v", err)
	}
	if len(inbox) != 1 || inbox[0].MessageID != message.ID {
		t.Fatalf("member inbox = %#v, want unread delivery for %q", inbox, message.ID)
	}
	reacted, err := database.AddSpaceMessageReaction(ctx, member.ID, project.ID, message.ID, "😂")
	if err != nil {
		t.Fatalf("AddSpaceMessageReaction(member) error = %v", err)
	}
	if len(reacted.Reactions) != 1 || reacted.Reactions[0].Emoji != "😂" || reacted.Reactions[0].Count != 1 || !reacted.Reactions[0].ReactedByMe {
		t.Fatalf("member reacted message = %#v, want one self reaction", reacted.Reactions)
	}
	if _, err := database.AddSpaceMessageReaction(ctx, member.ID, project.ID, message.ID, "😂"); err != nil {
		t.Fatalf("duplicate AddSpaceMessageReaction(member) error = %v", err)
	}
	ownerView, err := database.SpaceMessages(ctx, owner.ID, project.ID, 0, 20)
	if err != nil {
		t.Fatalf("SpaceMessages(owner after reaction) error = %v", err)
	}
	if len(ownerView) == 0 || len(ownerView[0].Reactions) != 1 || ownerView[0].Reactions[0].Count != 1 || ownerView[0].Reactions[0].ReactedByMe {
		t.Fatalf("owner reaction view = %#v, want count without self flag", ownerView)
	}
	unreacted, err := database.RemoveSpaceMessageReaction(ctx, member.ID, project.ID, message.ID, "😂")
	if err != nil {
		t.Fatalf("RemoveSpaceMessageReaction(member) error = %v", err)
	}
	if len(unreacted.Reactions) != 0 {
		t.Fatalf("removed reaction message = %#v, want no reactions", unreacted.Reactions)
	}
	events, _, err := database.SpaceEventsAfter(ctx, owner.ID, 0, 500)
	if err != nil {
		t.Fatalf("SpaceEventsAfter(owner) error = %v", err)
	}
	if !containsSpaceEvent(events, "message.reaction_added", message.ID) || !containsSpaceEvent(events, "message.reaction_removed", message.ID) {
		t.Fatalf("reaction events = %#v, want add and remove events for %q", events, message.ID)
	}

	if err := database.RemoveSpaceMember(ctx, owner.ID, project.ID, member.ID); err != nil {
		t.Fatalf("RemoveSpaceMember() error = %v", err)
	}
	projectAfterRemove, err := database.SpaceByID(ctx, owner.ID, project.ID)
	if err != nil || projectAfterRemove.IsShared || projectAfterRemove.MemberCount != 1 {
		t.Fatalf("Space after remove = %#v, %v, want private again", projectAfterRemove, err)
	}
	if err := database.DeleteSpace(ctx, owner.ID, project.ID, projectAfterRemove.Name); err != nil {
		t.Fatalf("DeleteSpace() error = %v", err)
	}
}

func TestOwnershipTransferRequiresRecipientStorageCapacity(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Transfer Owner", "transfer-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	recipient, err := database.CreateUser("Transfer Recipient", "transfer-recipient@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	recipientOwned, err := database.CreateSpace(ctx, recipient.ID, "Recipient workspace")
	if err != nil {
		t.Fatal(err)
	}
	project, err := database.CreateSpace(ctx, owner.ID, "Transfer project")
	if err != nil {
		t.Fatal(err)
	}
	invite, err := database.InviteToSpace(ctx, owner.ID, project.ID, recipient.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RespondToSpaceInvite(ctx, recipient.ID, invite.ID, true); err != nil {
		t.Fatal(err)
	}
	setUsage := func(spaceID string, used int64) {
		t.Helper()
		if err := database.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE space_storage_usage SET used_bytes=$2,version=version+1,updated_at=NOW() WHERE space_id=$1`, spaceID, used)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	setUsage(project.ID, 200_000_000)
	setUsage(recipientOwned.ID, 1_900_000_000)
	if err := database.TransferSpaceOwnership(ctx, owner.ID, project.ID, recipient.ID); !errors.Is(err, ErrLibraryQuota) {
		t.Fatalf("over-capacity transfer = %v, want ErrLibraryQuota", err)
	}
	setUsage(recipientOwned.ID, 1_700_000_000)
	if err := database.TransferSpaceOwnership(ctx, owner.ID, project.ID, recipient.ID); err != nil {
		t.Fatalf("transfer with capacity = %v", err)
	}
	transferred, err := database.SpaceByID(ctx, recipient.ID, project.ID)
	if err != nil || transferred.OwnerUserID != recipient.ID {
		t.Fatalf("transferred Space = %#v, %v", transferred, err)
	}
}

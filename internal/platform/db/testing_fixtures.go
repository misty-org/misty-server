package db

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

// Test fixtures: thin forwarders over the production entry points, kept so
// contract tests can set up spaces, messages and runs in one call.

func (db *Database) TestingCreateSpace(ctx context.Context, userID, name string) (*Space, error) {
	result, err := db.CreateSpace(ctx, userID, name, "")
	if err != nil {
		return nil, err
	}
	return &result.Space, nil
}

func (db *Database) TestingInviteToSpace(ctx context.Context, ownerID, spaceID, email string) (*SpaceInvitation, error) {
	return db.InviteToSpaceWithToken(
		ctx, ownerID, spaceID, email, "legacy-"+strings.ReplaceAll(uuid.NewString(), "-", ""),
	)
}

func (db *Database) TestingCreateSpaceMessage(ctx context.Context, userID, spaceID string, content []MessageSpan, fileNodeIDs []string) (*SpaceMessage, []string, error) {
	return db.TestingCreateSpaceMessageWithReferences(ctx, userID, spaceID, content, fileNodeIDs, nil, nil, "")
}

func (db *Database) TestingCreateSpaceMessageWithReferences(ctx context.Context, userID, spaceID string, content []MessageSpan, fileNodeIDs, attachmentIDs, libraryItemIDs []string, replyToMessageID string) (*SpaceMessage, []string, error) {
	return db.CreateSpaceMessageWithReferencesAndClientNonce(ctx, userID, spaceID, content, fileNodeIDs, attachmentIDs, libraryItemIDs, replyToMessageID, "")
}

func (db *Database) TestingCreateSpaceConversationMessageWithReferences(ctx context.Context, userID, spaceID, conversationID string, content []MessageSpan, fileNodeIDs, attachmentIDs, libraryItemIDs []string, replyToMessageID string) (*SpaceMessage, []string, error) {
	return db.CreateSpaceConversationMessageWithReferencesAndClientNonce(ctx, userID, spaceID, conversationID, content, fileNodeIDs, attachmentIDs, libraryItemIDs, replyToMessageID, "")
}

func TestingDefaultLibraryEditDefinition() LibraryEditDefinition {
	return LibraryEditDefinition{Brightness: 1, Contrast: 1, Saturation: 1, PlaybackSpeed: 1}
}

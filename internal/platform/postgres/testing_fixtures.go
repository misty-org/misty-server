package db

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// Test fixtures: thin forwarders over the production entry points, kept so
// contract tests can set up spaces, messages and runs in one call.

func (db *Database) TestingCreateSpace(ctx context.Context, userID, name string) (*Space, error) {
	result, err := db.TestingCreateSpaceWithTemplate(ctx, userID, name, "blank", nil)
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

func (db *Database) TestingCreateSpaceWithTemplate(ctx context.Context, userID, name, templateID string, providers []string) (*CreateSpaceResult, error) {
	return db.CreateSpaceWithTemplateIdempotent(ctx, userID, name, templateID, providers, "")
}

func (db *Database) TestingAppendAIInvocationEvent(ctx context.Context, userID, invocationID string, sequence int64, eventType string, payload json.RawMessage, state string) error {
	if sequence < 1 {
		return ErrSpaceInvalid
	}
	_, err := db.CommitAIInvocationEvent(ctx, userID, invocationID, "compat:"+strconv.FormatInt(sequence, 10), eventType, payload, state)
	return err
}

func (db *Database) TestingSearchSmartLibrary(userID, folderID, query string, limit int) ([]SmartLibrarySearchHit, error) {
	return db.SearchSmartLibraryHybrid(userID, folderID, query, nil, limit)
}

func (db *Database) TestingAwaitAIUserIntervention(ctx context.Context, user, run, runtime, call, hook, scope, action, reason, digest string) (*AIInterventionWait, error) {
	return db.AwaitAgentUserIntervention(ctx, user, run, runtime, call, hook, scope, action, reason, digest)
}

func (db *Database) TestingRecoverVoiceUsage(ctx context.Context) error {
	_, err := db.RecoverVoiceUsageBatch(ctx)
	return err
}

func TestingDefaultLibraryEditDefinition() LibraryEditDefinition {
	return LibraryEditDefinition{Brightness: 1, Contrast: 1, Saturation: 1, PlaybackSpeed: 1}
}

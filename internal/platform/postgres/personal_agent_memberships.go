package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// AskExecutionContext is a request-scoped view of the global identity. Space
// authority always comes from the requesting user, never an agent membership.
type AskExecutionContext struct {
	AgentID           string
	OwnerUserID       string
	SpaceID           string
	Name              string
	Instructions      string
	ModelID           string
	ReasoningEffort   string
	DefaultRunMode    string
	Enabled           bool
	ApprovedVersionID string
	ApprovedVersion   int64
	Permissions       json.RawMessage
	CanControl        bool
}

func (db *Database) AskExecutionContext(ctx context.Context, userID, spaceID, identityID string) (*AskExecutionContext, error) {
	var out *AskExecutionContext
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var err error
		out, err = askExecutionContextTx(ctx, tx, userID, spaceID, identityID)
		return err
	})
	return out, err
}

func askExecutionContextTx(ctx context.Context, tx *sql.Tx, userID, spaceID, identityID string) (*AskExecutionContext, error) {
	spaceID = "" // Agent identity does not depend on any content destination.
	if spaceID != "" {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return nil, err
		}
	}
	identity := &AskIdentity{}
	err := scanPersonalAgent(tx.QueryRowContext(ctx, `SELECT `+personalAgentColumns+` FROM misty_ask_identities WHERE id=$1 AND owner_user_id=$2 AND enabled AND deleted_at IS NULL`, identityID, userID), identity)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPersonalAgentNotFound
	}
	if err != nil {
		return nil, err
	}
	return &AskExecutionContext{AgentID: identity.ID, OwnerUserID: userID, SpaceID: spaceID,
		Name: identity.Name, Instructions: identity.Instructions, ModelID: identity.ModelID,
		ReasoningEffort: identity.ReasoningEffort, DefaultRunMode: identity.DefaultRunMode,
		Enabled: identity.Enabled, ApprovedVersionID: identity.LatestVersionID,
		ApprovedVersion: identity.Version, Permissions: json.RawMessage(`{}`), CanControl: true,
	}, nil
}

func (db *Database) EffectiveAgentSpacePermission(ctx context.Context, userID, spaceID, agentID, permission string) (bool, error) {
	allowed := false
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		memberAllowed, err := hasSpacePermissionTx(ctx, tx, userID, spaceID, permission)
		if err != nil || !memberAllowed {
			return err
		}
		if _, err := askExecutionContextTx(ctx, tx, userID, spaceID, agentID); err != nil {
			return err
		}
		allowed = memberAllowed
		return nil
	})
	return allowed, err
}

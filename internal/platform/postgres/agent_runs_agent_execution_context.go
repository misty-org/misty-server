package db

import (
	"context"
	"database/sql"
	"errors"
)


func sharedSpaceRunVisibleToUserTx(ctx context.Context, tx *sql.Tx, run *SpaceRun, userID string) (bool, error) {
	if run.RequestingMemberID == userID {
		return true, nil
	}
	if run.ConversationScopeKind == ConversationScopePrivate && run.ScopeConversationID != "" {
		var member bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_conversation_members cm JOIN space_conversations c ON c.id=cm.conversation_id WHERE cm.conversation_id=$1 AND cm.actor_kind='person' AND cm.user_id=$2 AND c.space_id=$3)`, run.ScopeConversationID, userID, run.SpaceID).Scan(&member); err != nil {
			return false, err
		}
		return member, nil
	}
	switch run.SourceType {
	case "schedule":
		return true, nil
	case "suggestion", "follow_up":
		return run.ConversationScopeKind == ConversationScopeEveryone, nil
	case "group_mention":
		var conversationExists, member bool
		if err := tx.QueryRowContext(ctx, `SELECT
			EXISTS(SELECT 1 FROM space_conversations c WHERE c.id=$1 AND c.space_id=$2),
			EXISTS(SELECT 1 FROM space_conversation_members cm JOIN space_conversations c ON c.id=cm.conversation_id WHERE cm.conversation_id=$1 AND cm.user_id=$3 AND c.space_id=$2)`, run.SourceConversationID, run.SpaceID, userID).Scan(&conversationExists, &member); err != nil {
			return false, err
		}
		if conversationExists {
			return member, nil
		}
		return true, nil // Everyone chat stores its source message ID here.
	default:
		return false, nil
	}
}

func (db *Database) SpaceRun(ctx context.Context, userID, runID string) (*SpaceRun, error) {
	out := &SpaceRun{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := scanSpaceRun(tx.QueryRowContext(ctx, `SELECT `+spaceRunColumns+` FROM space_runs WHERE id=$1`, runID), out); err != nil {
			return err
		}
		if out.AgentID != "" {
			if out.OwnerUserID != userID {
				return ErrSpaceForbidden
			}
			return nil
		}
		visible, err := sharedSpaceRunVisibleToUserTx(ctx, tx, out, userID)
		if err != nil {
			return err
		}
		if !visible {
			return ErrSpaceForbidden
		}
		if out.RequestingMemberID == userID {
			return requireSpacePermissionTx(ctx, tx, userID, out.SpaceID, PermissionAskRun)
		}
		return requireSpacePermissionTx(ctx, tx, userID, out.SpaceID, PermissionStudioView)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

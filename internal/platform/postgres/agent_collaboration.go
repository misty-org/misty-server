package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// Collaboration state on a Misty conversation: its Plan/Act mode, the
// questions an agent asked, the plans it proposed and the goal it pursues.
// Every row belongs to the conversation's owner; writes re-check ownership.

const (
	ConversationModeAct  = "act"
	ConversationModePlan = "plan"

	MaxAgentQuestions       = 4
	MaxAgentQuestionOptions = 4
	MaxAgentPlanSteps       = 12
	MaxGoalCriteria         = 10
)

var errConversationNotOwned = ErrSpaceNotFound

func conversationOwnedTx(ctx context.Context, tx *sql.Tx, user, conversation string) error {
	var owned bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM misty_ask_conversations WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL)`, conversation, user).Scan(&owned); err != nil {
		return err
	}
	if !owned {
		return errConversationNotOwned
	}
	return nil
}

// ConversationMode is the conversation's saved mode; a conversation without one acts.
func (db *Database) ConversationMode(ctx context.Context, user, conversation string) (string, error) {
	mode := ConversationModeAct
	if strings.TrimSpace(conversation) == "" {
		return mode, nil
	}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT mode FROM ai_conversation_modes WHERE conversation_id=$1 AND owner_user_id=$2`, conversation, user).Scan(&mode)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	})
	return mode, err
}

func (db *Database) SetConversationMode(ctx context.Context, user, conversation, mode string) error {
	if mode != ConversationModeAct && mode != ConversationModePlan {
		return ErrSpaceInvalid
	}
	return db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		if err := conversationOwnedTx(ctx, tx, user, conversation); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO ai_conversation_modes(conversation_id,owner_user_id,mode) VALUES($1,$2,$3)
			ON CONFLICT (conversation_id) DO UPDATE SET mode=EXCLUDED.mode, updated_at=now() WHERE ai_conversation_modes.owner_user_id=EXCLUDED.owner_user_id`,
			conversation, user, mode)
		return err
	})
}

func trimmedList(values []string, maxItems, maxRunes int) ([]string, error) {
	if len(values) > maxItems {
		return nil, ErrSpaceInvalid
	}
	out := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if len([]rune(value)) > maxRunes {
			return nil, ErrSpaceInvalid
		}
		out = append(out, value)
	}
	return out, nil
}

// RunningConversationInvocation is the conversation's in-flight invocation, or
// "" when none is running. Goal continuations start without the desktop, so it
// uses this to find and attach to their stream.
func (db *Database) RunningConversationInvocation(ctx context.Context, user, conversation string) (string, error) {
	id := ""
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT id FROM ai_invocations WHERE user_id=$1 AND conversation_id=$2
			AND state IN ('queued','running','awaiting_approval','awaiting_device','awaiting_intervention','awaiting_timer') ORDER BY created_at DESC LIMIT 1`, user, conversation).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	})
	return id, err
}

// AIInvocationState reads one invocation's state for its owner.
func (db *Database) AIInvocationState(ctx context.Context, user, id string) (string, error) {
	state := ""
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT state FROM ai_invocations WHERE id=$1 AND user_id=$2`, id, user).Scan(&state)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrSpaceNotFound
	}
	return state, err
}

// PendingAgentAppRequestForRun reports whether a run left an app card waiting.
func (db *Database) PendingAgentAppRequestForRun(ctx context.Context, user, run string) (bool, error) {
	pending := false
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_app_requests WHERE owner_user_id=$1 AND run_id=$2 AND state='pending' AND expires_at>now())`, user, run).Scan(&pending)
	})
	return pending, err
}

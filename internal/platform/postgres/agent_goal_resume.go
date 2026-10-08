package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// PauseAgentGoalUntil pauses a pursued goal at a usage limit and records when
// it resumes by itself.
func (db *Database) PauseAgentGoalUntil(ctx context.Context, user, id, reason string, resumeAt time.Time) (*AgentGoal, error) {
	report, _ := json.Marshal(map[string]any{"status": "paused", "summary": strings.TrimSpace(reason), "reported_at": time.Now().UTC(), "resume_at": resumeAt.UTC()})
	out := &AgentGoal{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		return scanAgentGoal(tx.QueryRowContext(ctx, `UPDATE agent_goals SET status='paused',last_report=$3,resume_at=$4,updated_at=now()
			WHERE id=$1 AND owner_user_id=$2 AND status='pursuing' RETURNING `+agentGoalColumns, id, user, report, resumeAt), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceConflict
	}
	return out, err
}

// DueGoalResume is a goal whose usage limit has refilled.
type DueGoalResume struct {
	User, GoalID, ConversationID, LastInvocationID string
}

// ClaimDueGoalResumes returns goals paused at a usage limit that is now past,
// switching them back to pursuing. Each is claimed once.
func (db *Database) ClaimDueGoalResumes(ctx context.Context, limit int) ([]DueGoalResume, error) {
	out := []DueGoalResume{}
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `UPDATE agent_goals SET status='pursuing',resume_at=NULL,updated_at=now()
			WHERE id IN (SELECT id FROM agent_goals WHERE status='paused' AND resume_at<=now() ORDER BY resume_at LIMIT $1 FOR UPDATE SKIP LOCKED)
			RETURNING owner_user_id,id,conversation_id,last_invocation_id`, min(max(limit, 1), 100))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var d DueGoalResume
			if err = rows.Scan(&d.User, &d.GoalID, &d.ConversationID, &d.LastInvocationID); err != nil {
				return err
			}
			out = append(out, d)
		}
		return rows.Err()
	})
	return out, err
}

// AgentGoalByID reads one of the account's goals.
func (db *Database) AgentGoalByID(ctx context.Context, user, id string) (*AgentGoal, error) {
	out := &AgentGoal{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		return scanAgentGoal(tx.QueryRowContext(ctx, `SELECT `+agentGoalColumns+` FROM agent_goals WHERE id=$1 AND owner_user_id=$2`, id, user), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

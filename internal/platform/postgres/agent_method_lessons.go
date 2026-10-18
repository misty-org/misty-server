package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// Steps an agent learned from running a saved workflow (docs/plans/agent-harness.md,
// phase 7). They belong to the agent, apart from the person's request, and are kept as
// versions so a worse version can be set aside without losing the one before.

// agentMethodLessonsKept is how many versions a workflow keeps.
const agentMethodLessonsKept = 10

// AgentMethodLesson is one version of a workflow's learned steps.
type AgentMethodLesson struct {
	Version int
	Steps   string
}

// AddAgentMethodLesson saves a new version of a workflow's learned steps and returns it.
func (db *Database) AddAgentMethodLesson(ctx context.Context, user, methodID, steps, invocationID string) (AgentMethodLesson, error) {
	steps = strings.TrimSpace(steps)
	if user == "" || methodID == "" || steps == "" || len(steps) > 4000 {
		return AgentMethodLesson{}, ErrSpaceInvalid
	}
	lesson := AgentMethodLesson{Steps: steps}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		var owned bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_methods WHERE id=$1 AND user_id=$2)`, methodID, user).Scan(&owned); err != nil {
			return err
		}
		if !owned {
			return ErrSpaceNotFound
		}
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0)+1 FROM agent_method_lessons WHERE method_id=$1 AND user_id=$2`, methodID, user).Scan(&lesson.Version); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO agent_method_lessons(method_id,user_id,version,steps,source_invocation_id)
 VALUES($1,$2,$3,$4,NULLIF($5,''))`, methodID, user, lesson.Version, steps, invocationID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM agent_method_lessons WHERE method_id=$1 AND user_id=$2 AND version <= $3`,
			methodID, user, lesson.Version-agentMethodLessonsKept)
		return err
	})
	return lesson, err
}

// LatestAgentMethodLesson returns a workflow's newest learned steps, or a zero lesson.
func (db *Database) LatestAgentMethodLesson(ctx context.Context, user, methodID string) (AgentMethodLesson, error) {
	var lesson AgentMethodLesson
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT version, steps FROM agent_method_lessons WHERE method_id=$1 AND user_id=$2
 ORDER BY version DESC LIMIT 1`, methodID, user).Scan(&lesson.Version, &lesson.Steps)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	})
	return lesson, err
}

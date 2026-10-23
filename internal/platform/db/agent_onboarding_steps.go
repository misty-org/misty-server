package db

import (
	"context"
	"database/sql"
)

// Get started steps recorded when the server accepts the request that does them.
const (
	OnboardingStepPageAsk     = "page_ask"
	OnboardingStepWorkflowRun = "workflow_run"
)

// RecordAgentOnboardingStep marks a Get started step done. Repeats keep the first time.
func (db *Database) RecordAgentOnboardingStep(ctx context.Context, userID, step string) error {
	return db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO agent_onboarding_steps (user_id, step) VALUES ($1, $2)
			ON CONFLICT (user_id, step) DO NOTHING
		`, userID, step)
		return err
	})
}

// AgentOnboardingSteps lists the Get started steps the account has done.
func (db *Database) AgentOnboardingSteps(ctx context.Context, userID string) ([]string, error) {
	steps := []string{}
	err := db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT step FROM agent_onboarding_steps WHERE user_id = $1 ORDER BY completed_at
		`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var step string
			if err := rows.Scan(&step); err != nil {
				return err
			}
			steps = append(steps, step)
		}
		return rows.Err()
	})
	return steps, err
}

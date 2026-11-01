package db

import (
	"context"
	"database/sql"
)

// AIModelRoute is one model a run thinks on: its agent model or its vision
// model. The desktop's runtime reports the device's models and the server
// records them on the run.
type AIModelRoute struct {
	Role      string `json:"role"`
	Model     string `json:"model"`
	Reasoning string `json:"reasoning"`
	Enabled   bool   `json:"enabled"`
}

// FreezeAIModelRoutes records a run's models the first time it asks. Later
// calls keep the frozen routes, so a running task never changes models.
func (db *Database) FreezeAIModelRoutes(ctx context.Context, user, run string, routes []AIModelRoute) ([]AIModelRoute, error) {
	result := []AIModelRoute{}
	err := db.WithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		for _, r := range routes {
			_, err := tx.ExecContext(ctx, `INSERT INTO ai_model_run_routes(owner_user_id,run_id,role,model,reasoning,enabled)
 VALUES($1,$2,$3,$4,$5,true) ON CONFLICT(run_id,role) DO NOTHING`, user, run, r.Role, r.Model, r.Reasoning)
			if err != nil {
				return err
			}
		}
		rows, err := tx.QueryContext(ctx, `SELECT role,model,reasoning,enabled FROM ai_model_run_routes WHERE owner_user_id=$1 AND run_id=$2 ORDER BY role`, user, run)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r AIModelRoute
			if err = rows.Scan(&r.Role, &r.Model, &r.Reasoning, &r.Enabled); err != nil {
				return err
			}
			result = append(result, r)
		}
		return rows.Err()
	})
	return result, err
}

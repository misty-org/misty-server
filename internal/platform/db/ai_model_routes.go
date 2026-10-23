package db

import (
	"context"
	"database/sql"
	"errors"

	"github.com/kannachi323/misty/server/internal/aimodels"
)

func (db *Database) AIModelRoutes(ctx context.Context, user string) ([]aimodels.Route, error) {
	result := []aimodels.Route{}
	err := db.WithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT role,model,reasoning,enabled FROM ai_model_routes WHERE owner_user_id=$1 ORDER BY role`, user)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r aimodels.Route
			if err = rows.Scan(&r.Role, &r.Model, &r.Reasoning, &r.Enabled); err != nil {
				return err
			}
			result = append(result, r)
		}
		return rows.Err()
	})
	return result, err
}

// Freeze all roles together: edits cannot change the model of a running task.
// A model the plan forces beats a model in overrides (a conversation's own
// pick), which beats the account's choice, which beats the default. Reasoning
// always comes from the admitted defaults.
func (db *Database) FreezeAIModelRoutes(ctx context.Context, user, run string, defaults []aimodels.Route, overrides map[string]string, forced ...map[string]string) ([]aimodels.Route, error) {
	planned := map[string]string{}
	for _, f := range forced {
		for role, model := range f {
			planned[role] = model
		}
	}
	result := []aimodels.Route{}
	err := db.WithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "ai-models:"+user); err != nil {
			return err
		}
		for _, d := range defaults {
			_, err := tx.ExecContext(ctx, `INSERT INTO ai_model_run_routes(owner_user_id,run_id,role,model,reasoning,enabled)
 SELECT $1,$2,$3,COALESCE(NULLIF($7,''),NULLIF($6,''),NULLIF(r.model,''),$4),$5,COALESCE(r.enabled,true) FROM (SELECT 1) seed LEFT JOIN ai_model_routes r ON r.owner_user_id=$1 AND r.role=$3
 ON CONFLICT(run_id,role) DO NOTHING`, user, run, d.Role, d.Model, d.Reasoning, overrides[d.Role], planned[d.Role])
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
			var r aimodels.Route
			if err = rows.Scan(&r.Role, &r.Model, &r.Reasoning, &r.Enabled); err != nil {
				return err
			}
			result = append(result, r)
		}
		return rows.Err()
	})
	return result, err
}

// AIModelBrowsingRoute is a run's browsing route. An empty browsing model, or
// a run frozen before the role existed, uses the run's vision route.
func (db *Database) AIModelBrowsingRoute(ctx context.Context, user, run string) (aimodels.Route, error) {
	route, err := db.AIModelRunRoute(ctx, user, run, "browsing")
	if err == nil && route.Model != "" {
		return route, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return route, err
	}
	vision, err := db.AIModelRunRoute(ctx, user, run, "vision")
	if err == nil {
		vision.Role = "browsing"
	}
	return vision, err
}

func (db *Database) AIModelRunRoute(ctx context.Context, user, run, role string) (aimodels.Route, error) {
	var r aimodels.Route
	err := db.WithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT role,model,reasoning,enabled FROM ai_model_run_routes WHERE owner_user_id=$1 AND run_id=$2 AND role=$3`, user, run, role).Scan(&r.Role, &r.Model, &r.Reasoning, &r.Enabled)
	})
	return r, err
}

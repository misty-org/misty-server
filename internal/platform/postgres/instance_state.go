package db

import (
	"context"
	"database/sql"
	"strings"

	"github.com/google/uuid"
)

type InstanceState struct {
	ServerID    string
	DisplayName string
}

// InstanceState creates the server identity once and returns it. The
// identifier is durable in PostgreSQL, so changing a hostname or restarting
// the stack keeps the same desktop credential namespace.
func (db *Database) InstanceState(ctx context.Context, displayName string) (InstanceState, error) {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		displayName = "Misty"
	}
	var state InstanceState
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO misty_instance (singleton, server_id, display_name)
			VALUES (TRUE, $1, $2)
			ON CONFLICT (singleton) DO UPDATE
			SET display_name = EXCLUDED.display_name, updated_at = NOW()
		`, "server_"+uuid.NewString(), displayName); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `
			SELECT server_id, display_name FROM misty_instance WHERE singleton = TRUE
		`).Scan(&state.ServerID, &state.DisplayName)
	})
	return state, err
}

package db

import (
	"context"
	"database/sql"
	"strings"

	"github.com/google/uuid"
)

type InstanceState struct {
	ServerID          string
	DisplayName       string
	BootstrapRequired bool
}

// SelfHostedInstanceState creates the instance identity once and returns the
// current bootstrap state in the same transaction. The identifier is durable
// in PostgreSQL, so changing a hostname or restarting the stack cannot merge
// its desktop credential namespace with another deployment.
func (db *Database) SelfHostedInstanceState(ctx context.Context, displayName string) (InstanceState, error) {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		displayName = "Misty Self-hosted"
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
		if err := tx.QueryRowContext(ctx, `
			SELECT server_id, display_name, NOT EXISTS (SELECT 1 FROM self_host_accounts)
			FROM misty_instance
			WHERE singleton = TRUE
		`).Scan(&state.ServerID, &state.DisplayName, &state.BootstrapRequired); err != nil {
			return err
		}
		return nil
	})
	return state, err
}

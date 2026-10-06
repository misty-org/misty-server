package db

import (
	"database/sql"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func reportConnectedProvider(t *testing.T, database *Database, user, provider, state string) error {
	t.Helper()
	return database.TestingWithRLSContext(t.Context(), TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE sdk_provider_registrations SET reported_state=$3,observed_at=now() WHERE user_id=$1 AND provider_id=$2`, user, provider, state)
		return err
	})
}

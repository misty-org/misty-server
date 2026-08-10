package db

import (
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestLatestMigrationVersionMatchesNewestMigrationFile(t *testing.T) {
	version, err := TestingLatestMigrationVersion()
	if err != nil {
		t.Fatalf("latestMigrationVersion() error = %v", err)
	}
	if version <= 0 {
		t.Fatalf("latestMigrationVersion() = %d, want a positive version", version)
	}
}

func TestCheckSchemaVersionPassesForFullyMigratedDatabase(t *testing.T) {
	database := openTestDatabase(t)

	if err := database.TestingCheckSchemaVersion(); err != nil {
		t.Fatalf("checkSchemaVersion() on a freshly migrated test database returned an error: %v", err)
	}
}

func TestCheckSchemaVersionDetectsADatabaseMissingTheLatestMigration(t *testing.T) {
	database := openTestDatabase(t)

	var latestVersion int64
	var latestTimestamp time.Time
	if err := database.Conn.QueryRow(
		`SELECT version_id, tstamp FROM goose_db_version WHERE is_applied = true ORDER BY version_id DESC LIMIT 1`,
	).Scan(&latestVersion, &latestTimestamp); err != nil {
		t.Fatalf("read latest applied migration: %v", err)
	}

	if _, err := database.Conn.Exec(`DELETE FROM goose_db_version WHERE version_id = $1`, latestVersion); err != nil {
		t.Fatalf("simulate a stale database: %v", err)
	}
	t.Cleanup(func() {
		if _, err := database.Conn.Exec(
			`INSERT INTO goose_db_version (version_id, is_applied, tstamp) VALUES ($1, true, $2)`,
			latestVersion, latestTimestamp,
		); err != nil {
			t.Fatalf("restore goose_db_version after test: %v", err)
		}
	})

	if err := database.TestingCheckSchemaVersion(); err == nil {
		t.Fatal("checkSchemaVersion() did not return an error for a database missing the latest migration")
	}
}

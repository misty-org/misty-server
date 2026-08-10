package db

import (
	"database/sql"
	"strings"
	"testing"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
	"github.com/kannachi323/misty/server/test/testkit"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"

	"github.com/kannachi323/misty/server/internal/platform/security"
	"github.com/lib/pq"
)

func TestSessionCRUDAndExpiry(t *testing.T) {
	database := openTestDatabase(t)

	user, err := database.CreateUser("Session User", "session@example.com", "password123")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	tokenHash := security.HashToken("session-token")
	if err := database.CreateSession(tokenHash, user.ID); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	userID, err := database.GetSessionUserID(tokenHash)
	if err != nil {
		t.Fatalf("GetSessionUserID() error = %v", err)
	}
	if userID != user.ID {
		t.Fatalf("GetSessionUserID() = %q, want %q", userID, user.ID)
	}

	if _, err := database.Conn.Exec(`UPDATE sessions SET expires_at = NOW() - INTERVAL '1 minute' WHERE token_hash = $1`, tokenHash); err != nil {
		t.Fatalf("expire session update error = %v", err)
	}

	userID, err = database.GetSessionUserID(tokenHash)
	if err != nil {
		t.Fatalf("GetSessionUserID() after expiry error = %v", err)
	}
	if userID != "" {
		t.Fatalf("GetSessionUserID() after expiry = %q, want empty", userID)
	}

	if err := database.DeleteSession(tokenHash); err != nil {
		t.Fatalf("DeleteSession() error = %v", err)
	}
}

func TestSessionLookupWorksWithRuntimeRLSRole(t *testing.T) {
	database := openTestDatabase(t)

	user, err := database.CreateUser("Runtime Session User", "runtime-session@example.com", "password123")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	tokenHash := security.HashToken("runtime-session-token")
	if err := database.CreateSession(tokenHash, user.ID); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	runtimeDatabase := openRuntimeRoleDatabase(t, database)
	userID, err := runtimeDatabase.GetSessionUserID(tokenHash)
	if err != nil {
		t.Fatalf("GetSessionUserID() with runtime RLS role error = %v", err)
	}
	if userID != user.ID {
		t.Fatalf("GetSessionUserID() with runtime RLS role = %q, want %q", userID, user.ID)
	}
}

func openRuntimeRoleDatabase(t *testing.T, adminDatabase *Database) *Database {
	t.Helper()

	runtimeUser := strings.TrimSpace(envconfig.Getenv("DB_USER"))
	runtimePassword := strings.TrimSpace(envconfig.Getenv("DB_PASSWORD"))
	if runtimeUser == "" || runtimePassword == "" {
		t.Skip("DB_USER and DB_PASSWORD are required to exercise the runtime RLS role")
	}
	if _, err := adminDatabase.Conn.Exec(
		"GRANT SELECT ON sessions TO " + pq.QuoteIdentifier(runtimeUser),
	); err != nil {
		t.Fatalf("grant runtime role session access error = %v", err)
	}

	conn, err := sql.Open("postgres", testkit.DatabaseDSN(t, runtimeUser, runtimePassword))
	if err != nil {
		t.Fatalf("sql.Open() runtime role error = %v", err)
	}
	if err := conn.Ping(); err != nil {
		_ = conn.Close()
		t.Fatalf("runtime role database ping failed: %v", err)
	}

	var bypassesRLS bool
	if err := conn.QueryRow(
		`SELECT rolsuper OR rolbypassrls FROM pg_roles WHERE rolname=current_user`,
	).Scan(&bypassesRLS); err != nil {
		_ = conn.Close()
		t.Fatalf("inspect runtime role error = %v", err)
	}
	if bypassesRLS {
		_ = conn.Close()
		t.Skip("DB_USER bypasses row-level security")
	}

	t.Cleanup(func() {
		_ = conn.Close()
	})
	return &Database{Conn: conn}
}

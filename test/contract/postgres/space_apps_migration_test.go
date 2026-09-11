package db

import (
	"context"
	"os"
	"strings"
	"testing"
)

// Recreate the pre-cutover installation foreign keys in a rollback-only
// transaction and run the migration's actual reset against pinned SDK records.
func TestSpaceAppsResetPreservesPinnedSDKRecords(t *testing.T) {
	database := openTestDatabase(t)
	user, err := database.CreateUser("Migration owner", "space-migration@example.invalid", "password123")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../../internal/platform/postgres/migrations/20270202000000_space_apps.sql")
	if err != nil {
		t.Fatal(err)
	}
	reset, _, ok := strings.Cut(string(raw), "CREATE TABLE space_app_installations")
	if !ok {
		t.Fatal("installation reset boundary missing")
	}
	ctx := context.Background()
	tx, err := database.Conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`SELECT set_config('app.rls_mode','service',true)`)
	exec(`ALTER TABLE sdk_provider_versions ADD CONSTRAINT sdk_provider_versions_installation_fkey
		FOREIGN KEY(user_id,app_id) REFERENCES user_app_installations(user_id,app_id) ON DELETE CASCADE;
		ALTER TABLE sdk_app_publishers ADD CONSTRAINT sdk_app_publishers_user_id_app_id_fkey
		FOREIGN KEY(user_id,app_id) REFERENCES user_app_installations(user_id,app_id) ON DELETE CASCADE`)
	exec(`INSERT INTO user_app_installations(user_id,app_id,installed_version) VALUES($1,'browser','1')`, user.ID)
	exec(`INSERT INTO sdk_app_publishers(user_id,app_id,public_key) VALUES($1,'browser',decode(repeat('00',32),'hex'))`, user.ID)
	exec(`INSERT INTO sdk_app_manifest_versions(user_id,app_id,app_version,digest,document,signature)
		VALUES($1,'browser','1',repeat('a',64),'{}',decode(repeat('00',64),'hex'))`, user.ID)
	exec(`INSERT INTO sdk_provider_versions(user_id,provider_id,version,app_id,definition) VALUES($1,'browser',1,'browser','{}')`, user.ID)
	exec(`INSERT INTO sdk_targets(user_id,id,revision) VALUES($1,'00000000-0000-4000-8000-000000000001',1)`, user.ID)
	exec(`INSERT INTO sdk_target_versions(user_id,id,revision,provider_id,provider_version,app_version,installed_at,target,capabilities,caller_apps)
		VALUES($1,'00000000-0000-4000-8000-000000000001',1,'browser',1,'1',NOW(),'{"binding":{"kind":"browser"}}','[]','[]')`, user.ID)
	exec(reset)
	for table, want := range map[string]int{
		"user_app_installations":    0,
		"sdk_app_publishers":        1,
		"sdk_app_manifest_versions": 1,
		"sdk_provider_versions":     1,
		"sdk_target_versions":       1,
	} {
		var got int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM "+table+" WHERE user_id=$1", user.ID).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s: got %d records, want %d", table, got, want)
		}
	}
}

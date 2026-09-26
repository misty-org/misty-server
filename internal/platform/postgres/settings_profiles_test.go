package db

import (
	"context"
	"database/sql"
	"errors"
	"github.com/google/uuid"
	"os"
	"sync"
	"testing"
)

func TestSettingsProfilesPostgres(t *testing.T) {
	dsn := os.Getenv("MISTY_SETTINGS_TEST_DSN")
	if dsn == "" {
		t.Skip("requires disposable misty_settings_test database")
	}
	conn, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetMaxOpenConns(1)
	var name string
	if err = conn.QueryRow(`SELECT current_database()`).Scan(&name); err != nil || name != "misty_settings_test" {
		t.Fatal("refusing non-test database", err)
	}
	schema := "settings_test_" + uuid.NewString()[:8]
	if _, err = conn.Exec(`CREATE SCHEMA ` + schema + `; SET search_path TO ` + schema + `; CREATE TABLE users(id text PRIMARY KEY); INSERT INTO users VALUES('owner'),('other');`); err != nil {
		t.Fatal(err)
	}
	defer conn.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
	migration, err := migrationFiles.ReadFile("migrations/20270927000000_settings_profiles.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(string(migration)); err != nil {
		t.Fatal(err)
	}
	database := &Database{Conn: conn}
	ctx := context.Background()
	id := uuid.NewString()
	p, err := database.CreateSettingsProfile(ctx, "owner", id, "Work", map[string]any{"browser.searchEngine": "google"})
	if err != nil {
		t.Fatal(err)
	}
	retry, err := database.CreateSettingsProfile(ctx, "owner", id, "Work", nil)
	if err != nil || retry.ID != p.ID {
		t.Fatal("create retry", err)
	}
	if profiles, err := database.SettingsProfiles(ctx, "other"); err != nil || len(profiles) != 0 {
		t.Fatal("account isolation", err)
	}
	patch := SettingsProfilePatch{MutationID: uuid.NewString(), Set: map[string]any{"browser.searchEngine": "bing"}}
	p, err = database.PatchSettingsProfile(ctx, "owner", id, patch)
	if err != nil {
		t.Fatal(err)
	}
	revision := p.Revision
	disjoint := SettingsProfilePatch{MutationID: uuid.NewString(), Set: map[string]any{"files.hidden": true}}
	p, err = database.PatchSettingsProfile(ctx, "owner", id, disjoint)
	if err != nil || p.Values["browser.searchEngine"] != "bing" {
		t.Fatal("disjoint merge", err)
	}
	late := SettingsProfilePatch{MutationID: uuid.NewString(), Set: map[string]any{"browser.searchEngine": "brave"}}
	p, err = database.PatchSettingsProfile(ctx, "owner", id, late)
	if err != nil {
		t.Fatal(err)
	}
	p, err = database.PatchSettingsProfile(ctx, "owner", id, patch)
	if err != nil || p.Values["browser.searchEngine"] != "brave" || p.Revision != revision+2 {
		t.Fatal("retry overwrote latest commit", p, err)
	}
	patch.Set["browser.searchEngine"] = "google"
	if _, err = database.PatchSettingsProfile(ctx, "owner", id, patch); !errors.Is(err, ErrSettingsProfileMutation) {
		t.Fatal("reused ID accepted", err)
	}
	if _, err = database.PatchSettingsProfile(ctx, "other", id, late); !errors.Is(err, ErrSettingsProfileNotFound) {
		t.Fatal("cross-account mutation", err)
	}
	// Two independent connections race; the row lock must retain both keys.
	peerConn, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer peerConn.Close()
	peerConn.SetMaxOpenConns(1)
	if _, err := peerConn.Exec(`SET search_path TO ` + schema); err != nil {
		t.Fatal(err)
	}
	peer := &Database{Conn: peerConn}
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	workers.Add(2)
	for i, database := range []*Database{database, peer} {
		go func(i int, database *Database) {
			defer workers.Done()
			<-start
			keys := []string{"spaces.agenda.tasks", "spaces.agenda.roadmap"}
			_, err := database.PatchSettingsProfile(ctx, "owner", id, SettingsProfilePatch{
				MutationID: uuid.NewString(), Set: map[string]any{keys[i]: false},
			})
			results <- err
		}(i, database)
	}
	close(start)
	workers.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	snapshots, err := database.SettingsProfiles(ctx, "owner")
	if err != nil || len(snapshots) != 1 || snapshots[0].Values["spaces.agenda.tasks"] != false || snapshots[0].Values["spaces.agenda.roadmap"] != false {
		t.Fatal("simultaneous edits lost a key", snapshots, err)
	}
	// Unknown newer fields survive an older client's key-level updates.
	conn.Exec(`UPDATE settings_profiles SET values_json=values_json || '{"future.preference":true}'::jsonb WHERE id=$1`, id)
	p, err = database.PatchSettingsProfile(ctx, "owner", id, SettingsProfilePatch{MutationID: uuid.NewString(), Unset: []string{"browser.searchEngine"}})
	if err != nil || p.Values["future.preference"] != true || p.Values["files.hidden"] != true {
		t.Fatal("reset damaged unrelated values", err)
	}
	if _, ok := p.Values["browser.searchEngine"]; ok {
		t.Fatal("reset not applied")
	}
	if err = database.DeleteSettingsProfile(ctx, "owner", id); err != nil {
		t.Fatal(err)
	}
	if _, err = database.PatchSettingsProfile(ctx, "owner", id, late); !errors.Is(err, ErrSettingsProfileNotFound) {
		t.Fatal("deleted profile resurrected", err)
	}
	if profiles, err := database.SettingsProfiles(ctx, "owner"); err != nil || len(profiles) != 0 {
		t.Fatal("deleted profile returned", err)
	}
}

package db

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

func TestOfficialAppInstallPinUninstallAndRestoreLifecycle(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUser("App owner", "app-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}

	installed, err := database.InstallUserApp(ctx, user.ID, "planner", "1.0.0", 1, []string{"spaces.read", "tasks.read", "tasks.write"})
	if err != nil {
		t.Fatal(err)
	}
	if installed.State != "installed" || !installed.Pinned || installed.DataDeletionAt != nil {
		t.Fatalf("installed app = %#v", installed)
	}

	unpinned, err := database.SetUserAppPinned(ctx, user.ID, "planner", false)
	if err != nil || unpinned.Pinned {
		t.Fatalf("SetUserAppPinned(false) = %#v, %v", unpinned, err)
	}
	updated, err := database.InstallUserApp(ctx, user.ID, "planner", "1.1.0", 1, []string{"spaces.read", "tasks.read", "tasks.write"})
	if err != nil || updated.Pinned || updated.PinRank != unpinned.PinRank || updated.InstalledVersion != "1.1.0" {
		t.Fatalf("updated app = %#v, %v; update must preserve pin state and rank", updated, err)
	}
	var updateEvents int
	if err := database.Conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM app_install_events WHERE user_id=$1 AND app_id='planner' AND event_type='updated'`, user.ID).Scan(&updateEvents); err != nil || updateEvents != 1 {
		t.Fatalf("updated audit events = %d, %v", updateEvents, err)
	}

	now := time.Date(2026, time.September, 3, 18, 0, 0, 0, time.UTC)
	recoverable, err := database.UninstallUserApp(ctx, user.ID, "planner", now)
	if err != nil {
		t.Fatal(err)
	}
	if recoverable.State != "recoverable" || recoverable.Pinned || recoverable.UninstalledAt == nil || recoverable.DataDeletionAt == nil {
		t.Fatalf("recoverable app = %#v", recoverable)
	}
	if !recoverable.UninstalledAt.Equal(now) || !recoverable.DataDeletionAt.Equal(now.Add(AppDataRecoveryPeriod)) {
		t.Fatalf("retention timestamps = %v, %v", recoverable.UninstalledAt, recoverable.DataDeletionAt)
	}

	// Retrying an uninstall is idempotent and must not extend the recovery window.
	retried, err := database.UninstallUserApp(ctx, user.ID, "planner", now.Add(24*time.Hour))
	if err != nil || retried.DataDeletionAt == nil || !retried.DataDeletionAt.Equal(now.Add(AppDataRecoveryPeriod)) {
		t.Fatalf("retry uninstall = %#v, %v", retried, err)
	}

	restored, err := database.InstallUserApp(ctx, user.ID, "planner", "1.0.0", 1, []string{"spaces.read", "tasks.read", "tasks.write"})
	if err != nil {
		t.Fatal(err)
	}
	if restored.State != "installed" || !restored.Pinned || restored.UninstalledAt != nil || restored.DataDeletionAt != nil {
		t.Fatalf("restored app = %#v", restored)
	}

	apps, err := database.UserApps(ctx, user.ID)
	if err != nil || len(apps) != 1 || apps[0].AppID != "planner" {
		t.Fatalf("UserApps() = %#v, %v", apps, err)
	}
}

func TestOfficialAppRuntimeSessionIsScopedAndRevokedOnUninstall(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUser("Runtime owner", "runtime-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space := createTestSpace(t, database, ctx, user.ID, "Personal")
	if _, err := database.InstallUserApp(ctx, user.ID, "journal", "1.0.0", 1, []string{"spaces.read", "notes.read"}); err != nil {
		t.Fatal(err)
	}
	token := "runtime-session-token"
	session, err := database.CreateAppRuntimeSession(ctx, user.ID, "journal", security.HashToken(token), space.ID, AppRuntimeSessionTTL)
	if err != nil {
		t.Fatal(err)
	}
	if session.AppID != "journal" || session.SpaceID != space.ID || len(session.Scopes) != 2 {
		t.Fatalf("runtime session = %#v", session)
	}
	resolved, err := database.AppRuntimeSessionByToken(ctx, security.HashToken(token))
	if err != nil || resolved == nil || resolved.UserID != user.ID || resolved.AppID != "journal" {
		t.Fatalf("AppRuntimeSessionByToken() = %#v, %v", resolved, err)
	}
	record, err := database.PutAppPersonalRecord(ctx, *resolved, "draft", json.RawMessage(`{"text":"private"}`))
	if err != nil || record.Key != "draft" {
		t.Fatalf("PutAppPersonalRecord() = %#v, %v", record, err)
	}
	records, err := database.AppPersonalRecords(ctx, *resolved)
	if err != nil || len(records) != 1 || records[0].Key != "draft" {
		t.Fatalf("AppPersonalRecords() = %#v, %v", records, err)
	}
	if _, err := database.UninstallUserApp(ctx, user.ID, "journal", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	resolved, err = database.AppRuntimeSessionByToken(ctx, security.HashToken(token))
	if err != nil || resolved != nil {
		t.Fatalf("uninstalled app session = %#v, %v, want revoked", resolved, err)
	}
}

func TestAppPermissionReductionRetiresTokenEvenAfterRegrant(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUser("Grant owner", "grant-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	initial := []string{"notes.read", "notes.write"}
	if _, err := database.InstallUserApp(ctx, user.ID, "journal", "1.0.0", 1, initial); err != nil {
		t.Fatal(err)
	}
	hash := security.HashToken("reduced-permission-token")
	if _, err := database.CreateAppRuntimeSession(ctx, user.ID, "journal", hash, "", AppRuntimeSessionTTL); err != nil {
		t.Fatal(err)
	}
	for _, grants := range [][]string{{"notes.read"}, initial} {
		if _, err := database.InstallUserApp(ctx, user.ID, "journal", "1.0.0", 2, grants); err != nil {
			t.Fatal(err)
		}
		if session, err := database.AppRuntimeSessionByToken(ctx, hash); err != nil || session != nil {
			t.Fatalf("retired token with grants %v resolved to %#v, %v", grants, session, err)
		}
	}
	if session, err := database.CreateAppRuntimeSession(ctx, user.ID, "journal", security.HashToken("renewed-permission-token"), "", AppRuntimeSessionTTL); err != nil || session == nil || len(session.Scopes) != 2 {
		t.Fatalf("fresh grant = %#v, %v", session, err)
	}
}

func TestOfficialAppPurgeDeletesPrivateDataAfterRecoveryWindow(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUser("Purge owner", "purge-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.InstallUserApp(ctx, user.ID, "planner", "1.0.0", 1, []string{"tasks.read"}); err != nil {
		t.Fatal(err)
	}
	token := "purge-runtime-session"
	session, err := database.CreateAppRuntimeSession(ctx, user.ID, "planner", security.HashToken(token), "", AppRuntimeSessionTTL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.PutAppPersonalRecord(ctx, *session, "view", json.RawMessage(`{"mode":"board"}`)); err != nil {
		t.Fatal(err)
	}
	uninstalledAt := time.Now().UTC().Add(-AppDataRecoveryPeriod - time.Hour)
	if _, err := database.UninstallUserApp(ctx, user.ID, "planner", uninstalledAt); err != nil {
		t.Fatal(err)
	}
	jobs, err := database.ClaimDueAppDataDeletionJobs(ctx, 10)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("ClaimDueAppDataDeletionJobs() = %#v, %v", jobs, err)
	}
	if err := database.CompleteAppDataDeletion(ctx, jobs[0], time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	apps, err := database.UserApps(ctx, user.ID)
	if err != nil || len(apps) != 0 {
		t.Fatalf("UserApps() after purge = %#v, %v", apps, err)
	}
	if resolved, err := database.AppRuntimeSessionByToken(ctx, security.HashToken(token)); err != nil || resolved != nil {
		t.Fatalf("runtime session after purge = %#v, %v", resolved, err)
	}
	clean, err := database.InstallUserApp(ctx, user.ID, "planner", "1.0.0", 1, []string{"tasks.read"})
	if err != nil || clean.State != "installed" {
		t.Fatalf("clean reinstall = %#v, %v", clean, err)
	}
	cleanSession, err := database.CreateAppRuntimeSession(ctx, user.ID, "planner", security.HashToken("clean-session"), "", AppRuntimeSessionTTL)
	if err != nil {
		t.Fatal(err)
	}
	if records, err := database.AppPersonalRecords(ctx, *cleanSession); err != nil || len(records) != 0 {
		t.Fatalf("personal records after clean reinstall = %#v, %v", records, err)
	}
}

func TestOfficialAppCannotPinMissingInstallation(t *testing.T) {
	database := openTestDatabase(t)
	user, err := database.CreateUser("No apps", "no-apps@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SetUserAppPinned(context.Background(), user.ID, "chat", true); !errors.Is(err, ErrAppNotInstalled) {
		t.Fatalf("SetUserAppPinned(missing) = %v, want ErrAppNotInstalled", err)
	}
}

func TestOfficialAppPurgeReclaimsAStaleRunningJob(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUser("Stale purge owner", "stale-purge@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.InstallUserApp(ctx, user.ID, "journal", "1.0.0", 1, []string{"notes.read"}); err != nil {
		t.Fatal(err)
	}
	uninstalledAt := time.Now().UTC().Add(-AppDataRecoveryPeriod - time.Hour)
	if _, err := database.UninstallUserApp(ctx, user.ID, "journal", uninstalledAt); err != nil {
		t.Fatal(err)
	}
	claimed, err := database.ClaimDueAppDataDeletionJobs(ctx, 10)
	if err != nil || len(claimed) != 1 || claimed[0].Attempts != 1 {
		t.Fatalf("initial claim = %#v, %v", claimed, err)
	}
	if _, err := database.Conn.ExecContext(ctx, `UPDATE app_data_deletion_jobs
		SET started_at=NOW()-INTERVAL '16 minutes' WHERE user_id=$1 AND app_id='journal'`, user.ID); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := database.ClaimDueAppDataDeletionJobs(ctx, 10)
	if err != nil || len(reclaimed) != 1 || reclaimed[0].Attempts != 2 {
		t.Fatalf("stale reclaim = %#v, %v", reclaimed, err)
	}
	staleCompletionErr := database.CompleteAppDataDeletion(ctx, claimed[0], time.Now().UTC())
	if !errors.Is(staleCompletionErr, ErrAppRuntimeForbidden) {
		t.Fatalf("stale completion error = %v, want ErrAppRuntimeForbidden", staleCompletionErr)
	}
	if err := database.FailAppDataDeletion(ctx, claimed[0], staleCompletionErr); err != nil {
		t.Fatal(err)
	}
	var installationState, jobState string
	if err := database.Conn.QueryRowContext(ctx, `SELECT i.state,j.state FROM user_app_installations i
		JOIN app_data_deletion_jobs j ON j.user_id=i.user_id AND j.app_id=i.app_id
		WHERE i.user_id=$1 AND i.app_id='journal'`, user.ID).Scan(&installationState, &jobState); err != nil {
		t.Fatal(err)
	}
	if installationState != "purging" || jobState != "running" {
		t.Fatalf("new lease was changed by stale worker: installation=%q job=%q", installationState, jobState)
	}
	if err := database.CompleteAppDataDeletion(ctx, reclaimed[0], time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}

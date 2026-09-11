package db

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

func TestAppRuntimeSessionCreationWithRuntimeRLSRole(t *testing.T) {
	database := openTestDatabase(t)
	ctx := t.Context()
	owner, err := database.CreateUser("Session owner", "app-session-owner@example.invalid", "password123")
	if err != nil {
		t.Fatal(err)
	}
	member, err := database.CreateUser("Session member", "app-session-member@example.invalid", "password123")
	if err != nil {
		t.Fatal(err)
	}
	outsider, err := database.CreateUser("Session outsider", "app-session-outsider@example.invalid", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space := createTestSpace(t, database, ctx, owner.ID, "Session space")
	invite, err := database.InviteToSpace(ctx, owner.ID, space.ID, member.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = database.RespondToSpaceInvite(ctx, member.ID, invite.ID, true); err != nil {
		t.Fatal(err)
	}
	spec := AppInstallSpec{ID: "planner", Version: "1", PermissionVersion: 1, Scopes: []string{"tasks.read"}}
	if _, err = database.InstallSpaceApp(ctx, owner.ID, space.ID, spec, nil); err != nil {
		t.Fatal(err)
	}
	runtime := openRuntimeRoleDatabase(t, database)
	for _, userID := range []string{owner.ID, member.ID} {
		token := security.HashToken("rls-session-" + userID)
		session, err := runtime.CreateAppRuntimeSession(ctx, userID, spec.ID, token, space.ID, AppRuntimeSessionTTL)
		if err != nil {
			t.Fatalf("member session under restricted role: %v", err)
		}
		if session.SpaceID != space.ID || len(session.Scopes) != 1 || session.Scopes[0] != "tasks.read" {
			t.Fatalf("unexpected authority: %#v", session)
		}
		if live, err := runtime.AppRuntimeSessionByToken(ctx, token); err != nil || live == nil {
			t.Fatalf("session lookup: %v %v", live, err)
		}
	}
	if _, err = runtime.CreateAppRuntimeSession(ctx, outsider.ID, spec.ID, security.HashToken("outsider"), space.ID, AppRuntimeSessionTTL); !errors.Is(err, ErrAppRuntimeForbidden) {
		t.Fatalf("nonmember admitted: %v", err)
	}
	if _, err = runtime.CreateAppRuntimeSession(ctx, member.ID, "absent", security.HashToken("absent"), space.ID, AppRuntimeSessionTTL); !errors.Is(err, ErrAppNotInstalled) {
		t.Fatalf("absent app admitted: %v", err)
	}

	// A concurrent removal must finish before issuance can observe authority.
	// Wait for PostgreSQL to report the blocked lock, not a scheduling delay.
	tx, err := database.Conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE space_app_installations SET state='recoverable',authority_generation=authority_generation+1 WHERE space_id=$1 AND app_id=$2`, space.ID, spec.ID); err != nil {
		t.Fatal(err)
	}
	issueCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	issued := make(chan error, 1)
	go func() {
		_, issueErr := runtime.CreateAppRuntimeSession(issueCtx, member.ID, spec.ID, security.HashToken("concurrent-removal"), space.ID, AppRuntimeSessionTTL)
		issued <- issueErr
	}()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
waiting:
	for {
		select {
		case issueErr := <-issued:
			t.Fatalf("issuance bypassed pending installation change: %v", issueErr)
		case <-issueCtx.Done():
			t.Fatal("session issuance did not reach installation lock")
		case <-ticker.C:
			var blocked bool
			if err := database.Conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%FROM space_app_installations%FOR SHARE%')`).Scan(&blocked); err != nil {
				t.Fatal(err)
			}
			if blocked {
				break waiting
			}
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if issueErr := <-issued; !errors.Is(issueErr, ErrAppNotInstalled) {
		t.Fatalf("concurrent removal admitted session: %v", issueErr)
	}
	if _, err = runtime.RemoveSpaceApp(ctx, owner.ID, space.ID, spec.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = runtime.CreateAppRuntimeSession(ctx, member.ID, spec.ID, security.HashToken("removed"), space.ID, AppRuntimeSessionTTL); !errors.Is(err, ErrAppNotInstalled) {
		t.Fatalf("removed app admitted: %v", err)
	}
	if _, err = runtime.InstallSpaceApp(ctx, owner.ID, space.ID, spec, nil); err != nil {
		t.Fatal(err)
	}
	for _, userID := range []string{owner.ID, member.ID} {
		if live, err := runtime.AppRuntimeSessionByToken(ctx, security.HashToken("rls-session-"+userID)); err != nil || live != nil {
			t.Fatalf("revoked session revived: %v %v", live, err)
		}
	}
}

package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/misty-org/misty-server/internal/accounts"
	. "github.com/misty-org/misty-server/internal/platform/db"
	"github.com/misty-org/misty-server/internal/platform/security"
)

func startDeviceSignIn(t *testing.T, database *Database, name string) (string, string) {
	t.Helper()
	id, err := accounts.NewDeviceSignInID()
	if err != nil {
		t.Fatal(err)
	}
	idHash := accounts.HashDeviceSignInID(id)
	code, expires, err := database.CreateDeviceSignIn(context.Background(), idHash, name)
	if err != nil {
		t.Fatalf("CreateDeviceSignIn() error = %v", err)
	}
	if until := time.Until(expires); until < 9*time.Minute || until > 11*time.Minute {
		t.Fatalf("request lives %v, want 10 minutes", until)
	}
	return idHash, code
}

func wrongCodeFor(code string) string {
	last := 'B'
	if code[len(code)-1] == 'B' {
		last = 'C'
	}
	return code[:len(code)-1] + string(last)
}

func TestDeviceSignInApprovesOnceAndDeliversASession(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUserWithUsername("device_ada", "device_ada@example.com", "password-long-enough")
	if err != nil {
		t.Fatal(err)
	}
	idHash, code := startDeviceSignIn(t, database, "misty-cli on studio-mac")
	changes, stop, err := database.WatchDeviceSignIn(ctx, idHash)
	if err != nil {
		t.Fatalf("WatchDeviceSignIn() error = %v", err)
	}
	defer stop()
	looked, err := database.LookupDeviceSignIn(ctx, code)
	if err != nil || looked.Name != "misty-cli on studio-mac" {
		t.Fatalf("LookupDeviceSignIn() = %#v, %v", looked, err)
	}
	if err := database.DecideDeviceSignIn(ctx, code, user.ID, true); err != nil {
		t.Fatalf("approve error = %v", err)
	}
	select {
	case <-changes:
	case <-time.After(10 * time.Second):
		t.Fatal("approval was not pushed to the waiting device")
	}
	if err := database.DecideDeviceSignIn(ctx, code, user.ID, true); !errors.Is(err, ErrDeviceCodeNotFound) {
		t.Fatalf("second approval error = %v, want not found", err)
	}
	request, found, err := database.DeviceSignInByID(ctx, idHash)
	if err != nil || !found || request.State != DeviceSignInApproved || request.ApprovedUserID != user.ID {
		t.Fatalf("DeviceSignInByID() = %#v, %v, %v", request, found, err)
	}
	sid := "device-sid"
	outcome, err := database.CompleteDeviceSignIn(ctx, idHash, user.ID, security.HashToken(sid), security.HashToken("refresh"), time.Now().Add(time.Hour))
	if err != nil || outcome != DeviceSignInDelivered {
		t.Fatalf("CompleteDeviceSignIn() = %v, %v", outcome, err)
	}
	again, err := database.CompleteDeviceSignIn(ctx, idHash, user.ID, security.HashToken("other"), security.HashToken("refresh2"), time.Now().Add(time.Hour))
	if err != nil || again != DeviceSignInGone {
		t.Fatalf("second delivery = %v, %v", again, err)
	}
	sessions, err := database.AccountSessions(ctx, user.ID, security.HashToken(sid))
	if err != nil || len(sessions) != 1 {
		t.Fatalf("AccountSessions() = %#v, %v", sessions, err)
	}
	if sessions[0].Name != "misty-cli on studio-mac" || !sessions[0].Current || sessions[0].ID != security.SessionPublicID(sid) {
		t.Fatalf("session = %#v", sessions[0])
	}
}

func TestDeviceSignInRefusesAfterFiveWrongCodes(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUserWithUsername("device_bo", "device_bo@example.com", "password-long-enough")
	if err != nil {
		t.Fatal(err)
	}
	idHash, code := startDeviceSignIn(t, database, "tests on ci")
	for try := 1; try <= accounts.DeviceSignInMaxWrongCodes; try++ {
		if err := database.DecideDeviceSignIn(ctx, wrongCodeFor(code), user.ID, true); !errors.Is(err, ErrDeviceCodeNotFound) {
			t.Fatalf("wrong code %d error = %v", try, err)
		}
	}
	request, _, err := database.DeviceSignInByID(ctx, idHash)
	if err != nil || request.State != DeviceSignInRefused {
		t.Fatalf("after five wrong codes state = %q, %v", request.State, err)
	}
	if err := database.DecideDeviceSignIn(ctx, code, user.ID, true); !errors.Is(err, ErrDeviceCodeNotFound) {
		t.Fatalf("the right code after refusal error = %v", err)
	}
}

func TestDeviceSignInRefusalAndUnknownCodes(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUserWithUsername("device_cy", "device_cy@example.com", "password-long-enough")
	if err != nil {
		t.Fatal(err)
	}
	idHash, code := startDeviceSignIn(t, database, "a script")
	unknown := "BBBBBBBB"
	if code[:4] == "BBBB" {
		unknown = "CCCCCCCC"
	}
	if _, err := database.LookupDeviceSignIn(ctx, unknown); !errors.Is(err, ErrDeviceCodeNotFound) {
		t.Fatalf("unknown code error = %v", err)
	}
	if err := database.DecideDeviceSignIn(ctx, code, user.ID, false); err != nil {
		t.Fatalf("refuse error = %v", err)
	}
	request, _, err := database.DeviceSignInByID(ctx, idHash)
	if err != nil || request.State != DeviceSignInRefused {
		t.Fatalf("state = %q, %v", request.State, err)
	}
	if _, err := database.Conn.Exec(`UPDATE device_sign_in_requests SET created_at=now()-interval '20 minutes',expires_at=now()-interval '11 minutes' WHERE id_hash=$1`, idHash); err != nil {
		t.Fatal(err)
	}
	if purged, err := database.PurgeExpiredDeviceSignIns(ctx, 100); err != nil || purged != 1 {
		t.Fatalf("PurgeExpiredDeviceSignIns() = %d, %v", purged, err)
	}
}

func TestSignOutOneSessionKeepsTheOthers(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	user, err := database.CreateUserWithUsername("sessions_di", "sessions_di@example.com", "password-long-enough")
	if err != nil {
		t.Fatal(err)
	}
	for _, sid := range []string{"laptop", "desktop"} {
		if err := database.CreateNamedRefreshSession(ctx, security.HashToken(sid), security.HashToken("r-"+sid), user.ID, "Misty on "+sid, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	found, err := database.SignOutAccountSession(ctx, user.ID, security.SessionPublicID("laptop"))
	if err != nil || !found {
		t.Fatalf("SignOutAccountSession() = %v, %v", found, err)
	}
	other, err := database.CreateUserWithUsername("sessions_ed", "sessions_ed@example.com", "password-long-enough")
	if err != nil {
		t.Fatal(err)
	}
	if found, err := database.SignOutAccountSession(ctx, other.ID, security.SessionPublicID("desktop")); err != nil || found {
		t.Fatalf("someone else signed out a session: %v, %v", found, err)
	}
	sessions, err := database.AccountSessions(ctx, user.ID, "")
	if err != nil || len(sessions) != 1 || sessions[0].Name != "Misty on desktop" || sessions[0].Current {
		t.Fatalf("AccountSessions() = %#v, %v", sessions, err)
	}
	before := sessions[0].LastUsedAt
	time.Sleep(10 * time.Millisecond)
	if ok, err := database.RotateRefreshSession(ctx, security.HashToken("desktop"), security.HashToken("r-desktop"), security.HashToken("r2"), user.ID); err != nil || !ok {
		t.Fatalf("RotateRefreshSession() = %v, %v", ok, err)
	}
	sessions, err = database.AccountSessions(ctx, user.ID, "")
	if err != nil || !sessions[0].LastUsedAt.After(before) {
		t.Fatalf("last use did not move on refresh: %v -> %#v, %v", before, sessions, err)
	}
}

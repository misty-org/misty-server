package db

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/kannachi323/misty/server/internal/platform/security"
)

// Runs against a disposable database with every migration applied.
func accountVerificationDB(t *testing.T) *Database {
	t.Helper()
	dsn := os.Getenv("MISTY_ACCOUNT_VERIFICATION_TEST_DSN")
	if dsn == "" {
		t.Skip("requires a disposable misty_account_verification_test PostgreSQL with migrations applied")
	}
	conn, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	var name string
	if err = conn.QueryRow(`SELECT current_database()`).Scan(&name); err != nil || name != "misty_account_verification_test" {
		t.Fatal("refusing a non-disposable database", name, err)
	}
	return &Database{Conn: conn}
}

func TestEmailVerificationAndReferralClaims(t *testing.T) {
	database := accountVerificationDB(t)
	ctx := context.Background()
	suffix := time.Now().Format("150405.000000")
	referrer, err := database.CreateUser("referrer-"+suffix+"@example.test", "password123")
	if err != nil {
		t.Fatal(err)
	}
	friend, err := database.CreateUser("friend-"+suffix+"@example.test", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if verified, err := database.EmailVerifiedAt(ctx, friend.ID); err != nil || verified != nil {
		t.Fatal("a new password account started verified", verified, err)
	}
	token := "verification-token-" + suffix
	if err = database.UpsertEmailVerificationToken(ctx, friend.ID, security.HashToken(token), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if user, err := database.VerifyEmailWithToken(ctx, security.HashToken(token), time.Now()); err != nil || user != friend.ID {
		t.Fatal(user, err)
	}
	if _, err = database.VerifyEmailWithToken(ctx, security.HashToken(token), time.Now()); !errors.Is(err, ErrEmailVerificationInvalid) {
		t.Fatal("a verification link worked twice", err)
	}
	if verified, _ := database.EmailVerifiedAt(ctx, friend.ID); verified == nil {
		t.Fatal("verification was not recorded")
	}

	code, err := database.ReferralCode(ctx, referrer.ID)
	if err != nil || len(code) != 8 {
		t.Fatal(code, err)
	}
	if again, _ := database.ReferralCode(ctx, referrer.ID); again != code {
		t.Fatal("referral code changed", again, code)
	}
	if err = database.RecordSignupSignals(ctx, referrer.ID, "network-a", "device-a"); err != nil {
		t.Fatal(err)
	}
	if _, err = database.ClaimReferral(ctx, referrer.ID, code, "", "", time.Now()); !errors.Is(err, ErrReferralNotClaimable) {
		t.Fatal("an account referred itself", err)
	}
	if _, err = database.ClaimReferral(ctx, friend.ID, "zzzzzzzz", "", "", time.Now()); !errors.Is(err, ErrReferralCodeUnknown) {
		t.Fatal("an unknown code was accepted", err)
	}
	claim, err := database.ClaimReferral(ctx, friend.ID, code, "network-a", "device-b", time.Now())
	if err != nil || claim.ReferrerID != referrer.ID || !claim.SameNetwork || claim.SameDevice {
		t.Fatal(claim, err)
	}
	if _, err = database.ClaimReferral(ctx, friend.ID, code, "", "", time.Now()); !errors.Is(err, ErrReferralNotClaimable) {
		t.Fatal("a second claim was accepted", err)
	}
	if _, err = database.ClaimReferral(ctx, friend.ID, code, "", "", time.Now().Add(8*24*time.Hour)); !errors.Is(err, ErrReferralNotClaimable) {
		t.Fatal("an old account claimed a referral", err)
	}
	pending, err := database.PendingReferralClaim(ctx, friend.ID)
	if err != nil || pending == nil || pending.Sent {
		t.Fatal(pending, err)
	}
	if err = database.MarkReferralClaimSent(ctx, friend.ID); err != nil {
		t.Fatal(err)
	}
	if pending, _ = database.PendingReferralClaim(ctx, friend.ID); !pending.Sent {
		t.Fatal("claim not marked sent")
	}
}

func TestGoalPausedAtALimitResumesOnce(t *testing.T) {
	database := accountVerificationDB(t)
	ctx := context.Background()
	if due, err := database.ClaimDueGoalResumes(ctx, 10); err != nil || len(due) != 0 {
		t.Fatal("unexpected due goals", due, err)
	}
	if _, err := database.PauseAgentGoalUntil(ctx, "nobody", "missing-goal", "limit", time.Now()); !errors.Is(err, ErrSpaceConflict) {
		t.Fatal("pausing a missing goal", err)
	}
}

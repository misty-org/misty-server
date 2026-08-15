package db

import (
	"errors"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

func TestSelfHostBootstrapEnrollmentRevocationAndRecovery(t *testing.T) {
	database := openTestDatabase(t)
	now := time.Now().UTC()
	bootstrapToken := "bootstrap-proof"
	if err := database.CreateSelfHostBootstrapToken(
		t.Context(), security.HashToken(bootstrapToken), now.Add(30*time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	admin, err := database.CreateSelfHostBootstrapAdmin(
		t.Context(), "Local Admin", "local_admin", "local-admin@example.com", "password123",
		security.HashToken(bootstrapToken), "license_subject_admin", now.Add(24*time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateSelfHostBootstrapAdmin(
		t.Context(), "Replay", "replay_admin", "replay@example.com", "password123",
		security.HashToken(bootstrapToken), "license_subject_replay", now.Add(24*time.Hour),
	); !errors.Is(err, ErrSelfHostBootstrapInvalid) {
		t.Fatalf("bootstrap replay error = %v", err)
	}

	revokedToken := "revoked-enrollment"
	if err := database.CreateSelfHostInvitation(
		t.Context(), admin.ID, "enrollment_00000000-0000-0000-0000-000000000001",
		security.HashToken(revokedToken), now.Add(7*24*time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	if err := database.RevokeSelfHostInvitation(
		t.Context(), admin.ID, "enrollment_00000000-0000-0000-0000-000000000001",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateSelfHostEnrolledUser(
		t.Context(), "Revoked User", "revoked_user", "revoked@example.com", "password123",
		security.HashToken(revokedToken), "license_subject_revoked", now.Add(24*time.Hour),
	); !errors.Is(err, ErrSelfHostInviteInvalid) {
		t.Fatalf("revoked invitation error = %v", err)
	}

	enrollmentToken := "valid-enrollment"
	if err := database.CreateSelfHostInvitation(
		t.Context(), admin.ID, "enrollment_00000000-0000-0000-0000-000000000002",
		security.HashToken(enrollmentToken), now.Add(7*24*time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	member, err := database.CreateSelfHostEnrolledUser(
		t.Context(), "Local Member", "local_member", "local-member@example.com", "password123",
		security.HashToken(enrollmentToken), "license_subject_member", now.Add(24*time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateSelfHostEnrolledUser(
		t.Context(), "Replay Member", "replay_member", "replay-member@example.com", "password123",
		security.HashToken(enrollmentToken), "license_subject_replay_member", now.Add(24*time.Hour),
	); !errors.Is(err, ErrSelfHostInviteInvalid) {
		t.Fatalf("enrollment replay error = %v", err)
	}

	sessionHash := security.HashToken("local-member-session")
	if err := database.CreateSession(sessionHash, member.ID); err != nil {
		t.Fatal(err)
	}
	if err := database.ResetSelfHostPassword(t.Context(), member.Email, "replacement123"); err != nil {
		t.Fatal(err)
	}
	if userID, err := database.GetSessionUserID(sessionHash); err != nil || userID != "" {
		t.Fatalf("session after password reset = %q, %v", userID, err)
	}
	if err := database.DisableSelfHostAccount(t.Context(), member.Email); err != nil {
		t.Fatal(err)
	}
	access, err := database.SelfHostAccountAccess(t.Context(), member.ID)
	if err != nil || !access.Disabled {
		t.Fatalf("disabled access = %#v, %v", access, err)
	}
}

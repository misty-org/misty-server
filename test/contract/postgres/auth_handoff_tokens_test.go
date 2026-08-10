package db

import (
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"

	"github.com/kannachi323/misty/server/internal/platform/security"
)

func TestAuthHandoffTokenIsConsumedOnFirstRead(t *testing.T) {
	database := openTestDatabase(t)

	user, err := database.CreateUser("Handoff User", "handoff-consume@example.com", "password")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	tokenHash := security.HashToken("handoff-token")
	if err := database.CreateAuthHandoffToken(user.ID, tokenHash, "/settings/billing", time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("CreateAuthHandoffToken() error = %v", err)
	}

	gotUserID, gotPath, err := database.ConsumeAuthHandoffToken(tokenHash, time.Now())
	if err != nil {
		t.Fatalf("ConsumeAuthHandoffToken() error = %v", err)
	}
	if gotUserID != user.ID {
		t.Fatalf("ConsumeAuthHandoffToken() userID = %q, want %q", gotUserID, user.ID)
	}
	if gotPath != "/settings/billing" {
		t.Fatalf("ConsumeAuthHandoffToken() path = %q, want %q", gotPath, "/settings/billing")
	}

	// The whole point of this table: a replayed link must not mint a second
	// session.
	if _, _, err := database.ConsumeAuthHandoffToken(tokenHash, time.Now()); err != ErrAuthHandoffTokenInvalid {
		t.Fatalf("second ConsumeAuthHandoffToken() error = %v, want %v", err, ErrAuthHandoffTokenInvalid)
	}
}

func TestAuthHandoffTokenRejectsExpiredAndDeletesIt(t *testing.T) {
	database := openTestDatabase(t)

	user, err := database.CreateUser("Expired Handoff", "handoff-expired@example.com", "password")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	tokenHash := security.HashToken("expired-handoff-token")
	if err := database.CreateAuthHandoffToken(user.ID, tokenHash, "/settings", time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("CreateAuthHandoffToken() error = %v", err)
	}

	if _, _, err := database.ConsumeAuthHandoffToken(tokenHash, time.Now()); err != ErrAuthHandoffTokenInvalid {
		t.Fatalf("ConsumeAuthHandoffToken() error = %v, want %v", err, ErrAuthHandoffTokenInvalid)
	}
	// Expired rows are removed on the way out rather than lingering.
	if _, _, err := database.ConsumeAuthHandoffToken(tokenHash, time.Now()); err != ErrAuthHandoffTokenInvalid {
		t.Fatalf("second ConsumeAuthHandoffToken() error = %v, want %v", err, ErrAuthHandoffTokenInvalid)
	}
}

func TestAuthHandoffTokenUnknownHashIsInvalid(t *testing.T) {
	database := openTestDatabase(t)

	if _, _, err := database.ConsumeAuthHandoffToken(security.HashToken("never-issued"), time.Now()); err != ErrAuthHandoffTokenInvalid {
		t.Fatalf("ConsumeAuthHandoffToken() error = %v, want %v", err, ErrAuthHandoffTokenInvalid)
	}
}

func TestAuthHandoffSessionTTLIsShorterThanPasswordSession(t *testing.T) {
	// A handoff proves the user was signed in on the desktop, not that they just
	// typed a password in this browser. It should not mint a 30-day session.
	if AuthHandoffSessionTTL >= SessionTTL {
		t.Fatalf("AuthHandoffSessionTTL = %v, want less than SessionTTL %v", AuthHandoffSessionTTL, SessionTTL)
	}
}

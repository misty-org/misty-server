package api

import (
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

func TestNormalizeWaitlistEmail(t *testing.T) {
	email, err := TestingNormalizeWaitlistEmail("  User+alias@Example.COM ")
	if err != nil {
		t.Fatalf("normalizeWaitlistEmail() error = %v", err)
	}
	if email != "user+alias@example.com" {
		t.Fatalf("normalizeWaitlistEmail() = %q, want %q", email, "user+alias@example.com")
	}
}

func TestNormalizeWaitlistEmailRejectsInvalid(t *testing.T) {
	if _, err := TestingNormalizeWaitlistEmail("not-an-email"); err == nil {
		t.Fatal("normalizeWaitlistEmail() succeeded for invalid address")
	}
}

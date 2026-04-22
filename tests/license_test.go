package tests

import (
	"os"
	"testing"

	license "github.com/kannachi323/misty/server/core/license"
	"github.com/kannachi323/misty/server/db"
)

func TestIssueAndValidatePreservesTier(t *testing.T) {
	t.Setenv("LICENSE_SECRET", "test-secret")

	user := &db.User{
		ID:    "user_123",
		Email: "max@example.com",
	}
	sub := &db.Subscription{
		UserID: user.ID,
		Tier:   db.TierMax,
		Status: "active",
	}

	token, err := license.Issue(user, sub)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	claims, err := license.Validate(token)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	if claims.UserID != user.ID {
		t.Fatalf("claims.UserID = %q, want %q", claims.UserID, user.ID)
	}
	if claims.Email != user.Email {
		t.Fatalf("claims.Email = %q, want %q", claims.Email, user.Email)
	}
	if claims.Tier != db.TierMax {
		t.Fatalf("claims.Tier = %q, want %q", claims.Tier, db.TierMax)
	}
}

func TestValidateRejectsWrongSecret(t *testing.T) {
	t.Setenv("LICENSE_SECRET", "secret-a")

	token, err := license.Issue(&db.User{ID: "user_1", Email: "pro@example.com"}, &db.Subscription{
		UserID: "user_1",
		Tier:   db.TierPro,
		Status: "active",
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	if err := os.Setenv("LICENSE_SECRET", "secret-b"); err != nil {
		t.Fatalf("Setenv() error = %v", err)
	}

	if _, err := license.Validate(token); err == nil {
		t.Fatal("Validate() succeeded with wrong secret, want error")
	}
}

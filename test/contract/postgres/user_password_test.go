package db

import (
	"context"
	"testing"
)

func TestVerifyUserPassword(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, _ := database.CreateUser("password-owner@example.com", "password123")
	if valid, err := database.VerifyUserPassword(ctx, owner.ID, "password123"); err != nil || !valid {
		t.Fatalf("VerifyUserPassword(valid) = %v, %v", valid, err)
	}
	if valid, err := database.VerifyUserPassword(ctx, owner.ID, "wrong-password"); err != nil || valid {
		t.Fatalf("VerifyUserPassword(invalid) = %v, %v", valid, err)
	}
}

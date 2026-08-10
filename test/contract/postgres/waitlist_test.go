package db

import (
	"testing"
)

func TestCreateWaitlistSignupDuplicateReturnsFalse(t *testing.T) {
	database := openTestDatabase(t)

	created, err := database.CreateWaitlistSignup("Ada", "ada@example.com")
	if err != nil {
		t.Fatalf("CreateWaitlistSignup() error = %v", err)
	}
	if !created {
		t.Fatal("first waitlist signup should be created")
	}

	created, err = database.CreateWaitlistSignup("Ada Again", "ada@example.com")
	if err != nil {
		t.Fatalf("CreateWaitlistSignup() duplicate error = %v", err)
	}
	if created {
		t.Fatal("duplicate waitlist signup should return created=false")
	}
}

package api

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

type accountsExist bool

func (a accountsExist) AnyAccount(context.Context) (bool, error) { return bool(a), nil }

func TestRegistrationModes(t *testing.T) {
	ctx := context.Background()
	t.Setenv("MISTY_REGISTRATION", "")
	t.Setenv("MISTY_ENVIRONMENT", "development")
	if RegistrationMode() != "open" {
		t.Fatal("development defaults to open")
	}
	t.Setenv("MISTY_ENVIRONMENT", "production")
	if RegistrationMode() != "first" {
		t.Fatal("production defaults to first")
	}
	if allowed, _ := registrationAllowed(ctx, accountsExist(false)); !allowed {
		t.Fatal("the first account was refused")
	}
	if allowed, _ := registrationAllowed(ctx, accountsExist(true)); allowed || registrationState(ctx, accountsExist(true)) != "closed" {
		t.Fatal("a second account was allowed under first")
	}
	t.Setenv("MISTY_REGISTRATION", "open")
	if allowed, _ := registrationAllowed(ctx, accountsExist(true)); !allowed {
		t.Fatal("open refused an account")
	}
	t.Setenv("MISTY_REGISTRATION", "closed")
	if allowed, _ := registrationAllowed(ctx, accountsExist(false)); allowed {
		t.Fatal("closed allowed an account")
	}
	recorder := httptest.NewRecorder()
	writeRegistrationClosed(recorder)
	if recorder.Code != 403 || !strings.Contains(recorder.Body.String(), `"code":"registration_closed"`) {
		t.Fatalf("closed response = %d %s", recorder.Code, recorder.Body)
	}
}

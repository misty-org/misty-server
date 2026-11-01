package api

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeAccounts struct {
	accounts bool
	invited  map[string]bool
}

func (s fakeAccounts) AnyAccount(context.Context) (bool, error) { return s.accounts, nil }
func (s fakeAccounts) PendingAccountInvitation(_ context.Context, email string) (bool, error) {
	return s.invited[email], nil
}

func TestRegistrationFirstAccountThenInvitationOnly(t *testing.T) {
	ctx := context.Background()
	fresh := fakeAccounts{}
	if allowed, _ := registrationAllowed(ctx, fresh, "owner@example.com"); !allowed || registrationState(ctx, fresh) != "open" {
		t.Fatal("the first account was refused")
	}
	used := fakeAccounts{accounts: true, invited: map[string]bool{"guest@example.com": true}}
	if allowed, _ := registrationAllowed(ctx, used, "stranger@example.com"); allowed {
		t.Fatal("an uninvited second account was allowed")
	}
	if allowed, _ := registrationAllowed(ctx, used, "guest@example.com"); !allowed {
		t.Fatal("an invited account was refused")
	}
	if registrationState(ctx, used) != "invitation" {
		t.Fatalf("state = %q", registrationState(ctx, used))
	}
	recorder := httptest.NewRecorder()
	writeRegistrationClosed(recorder)
	if recorder.Code != 403 || !strings.Contains(recorder.Body.String(), `"code":"registration_closed"`) {
		t.Fatalf("closed response = %d %s", recorder.Code, recorder.Body)
	}
}

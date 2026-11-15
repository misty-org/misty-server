package api

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/misty-org/misty-server/internal/platform/db"
)

type fakeAccounts struct {
	accounts bool
	invited  map[string]bool
}

func (s fakeAccounts) AnyAccount(context.Context) (bool, error) { return s.accounts, nil }
func (s fakeAccounts) PendingAccountInvitation(_ context.Context, email string) (bool, error) {
	return s.invited[email], nil
}

type fakeOwner string

func TestAllowlistDecidesWhoSignsUp(t *testing.T) {
	t.Setenv("MISTY_ALLOWED_EMAILS", " Listed@Example.com, second@example.com\nthird@example.com ")
	t.Setenv("MISTY_OPEN_SIGNUP", "")
	ctx := context.Background()
	used := fakeAccounts{accounts: true, invited: map[string]bool{"invited@example.com": true}}
	for _, email := range []string{"listed@example.com", "LISTED@example.com ", "second@example.com", "third@example.com"} {
		if allowed, _ := registrationAllowed(ctx, used, email); !allowed {
			t.Fatalf("%q is on the list but was refused", email)
		}
	}
	for _, email := range []string{"stranger@example.com", "invited@example.com"} {
		if allowed, _ := registrationAllowed(ctx, used, email); allowed {
			t.Fatalf("%q isn't on the list but was allowed", email)
		}
	}
	if allowed, _ := registrationAllowed(ctx, fakeAccounts{}, "owner@example.com"); !allowed {
		t.Fatal("the first account was refused")
	}
	recorder := httptest.NewRecorder()
	writeRegistrationClosed(recorder)
	if recorder.Code != 403 || !strings.Contains(recorder.Body.String(), `"code":"email_not_allowed"`) {
		t.Fatalf("closed response = %d %s", recorder.Code, recorder.Body)
	}
}

func TestAllowlistDecidesWhoSignsIn(t *testing.T) {
	ctx := context.Background()
	stranger := &db.User{ID: "stranger", Email: "stranger@example.com"}
	listed := &db.User{ID: "listed", Email: "LISTED@example.com"}
	owner := &db.User{ID: "owner", Email: "owner@example.com"}
	store := fakeOwner("owner")
	t.Setenv("MISTY_OPEN_SIGNUP", "")

	t.Setenv("MISTY_ALLOWED_EMAILS", "")
	if allowed, _ := signInAllowed(ctx, store, stranger); !allowed {
		t.Fatal("with no list, anyone with an account signs in")
	}
	t.Setenv("MISTY_ALLOWED_EMAILS", "listed@example.com")
	if allowed, _ := signInAllowed(ctx, store, stranger); allowed {
		t.Fatal("an account off the list signed in")
	}
	if allowed, _ := signInAllowed(ctx, store, listed); !allowed {
		t.Fatal("an account on the list was refused")
	}
	if allowed, _ := signInAllowed(ctx, store, owner); !allowed {
		t.Fatal("the owner was locked out")
	}
}

func TestOpenSignupIgnoresTheAllowlist(t *testing.T) {
	t.Setenv("MISTY_ALLOWED_EMAILS", "listed@example.com")
	t.Setenv("MISTY_OPEN_SIGNUP", "1")
	ctx := context.Background()
	if allowed, _ := registrationAllowed(ctx, fakeAccounts{accounts: true}, "anyone@example.com"); !allowed {
		t.Fatal("open signup refused someone off the list")
	}
	stranger := &db.User{ID: "stranger", Email: "anyone@example.com"}
	if allowed, _ := signInAllowed(ctx, fakeOwner("owner"), stranger); !allowed {
		t.Fatal("open signup refused a sign-in off the list")
	}
}

func TestRegistrationFirstAccountThenInvitationOnly(t *testing.T) {
	ctx := context.Background()
	fresh := fakeAccounts{}
	if allowed, _ := registrationAllowed(ctx, fresh, "owner@example.com"); !allowed {
		t.Fatal("the first account was refused")
	}
	used := fakeAccounts{accounts: true, invited: map[string]bool{"guest@example.com": true}}
	if allowed, _ := registrationAllowed(ctx, used, "stranger@example.com"); allowed {
		t.Fatal("an uninvited second account was allowed")
	}
	if allowed, _ := registrationAllowed(ctx, used, "guest@example.com"); !allowed {
		t.Fatal("an invited account was refused")
	}
	recorder := httptest.NewRecorder()
	writeRegistrationClosed(recorder)
	if recorder.Code != 403 || !strings.Contains(recorder.Body.String(), `"code":"registration_closed"`) {
		t.Fatalf("closed response = %d %s", recorder.Code, recorder.Body)
	}
}

func (owner fakeOwner) IsServerOwner(_ context.Context, id string) (bool, error) {
	return string(owner) == id, nil
}

func TestSignupLimitsComeFromTheEnvironment(t *testing.T) {
	t.Setenv("MISTY_SIGNUP_LIMITS", "")
	closed, err := signupLimits()
	if err != nil || closed.StorageBytes == nil || *closed.StorageBytes != 0 || closed.Devices == nil || *closed.Devices != 0 || closed.Spaces != nil {
		t.Fatalf("no setting = %+v %v", closed, err)
	}
	t.Setenv("MISTY_SIGNUP_LIMITS", `{"storage_bytes":5000000000,"devices":null,"spaces":2}`)
	free, err := signupLimits()
	if err != nil || free.StorageBytes == nil || *free.StorageBytes != 5_000_000_000 || free.Devices != nil || free.Spaces == nil || *free.Spaces != 2 {
		t.Fatalf("free plan = %+v %v", free, err)
	}
	for _, bad := range []string{`{"storage":1}`, `{"spaces":-1}`, `not json`} {
		t.Setenv("MISTY_SIGNUP_LIMITS", bad)
		if _, err := signupLimits(); err == nil {
			t.Fatalf("%s was accepted", bad)
		}
	}
}

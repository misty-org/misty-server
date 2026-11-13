package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeAccountStates struct{ closing map[string]time.Time }

func (fakeAccountStates) SuspendedAccounts(context.Context) ([]string, error) { return nil, nil }
func (f fakeAccountStates) ClosingAccounts(context.Context) (map[string]time.Time, error) {
	return f.closing, nil
}

func TestReadOnlyRequestsAreReadsTheListedPostsAndThePassword(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		allowed      bool
	}{
		{http.MethodGet, "/v1/spaces/s1/library/items/i1/download", true},
		{http.MethodHead, "/v1/me", true},
		{http.MethodPost, "/v1/me/export", true},
		{http.MethodPost, "/api/auth/refresh", true},
		{http.MethodPost, "/logout", true},
		{http.MethodPost, "/v1/realtime/tickets", true},
		{http.MethodPost, "/v1/spaces/s1/notes/n1/collaboration-ticket", true},
		{http.MethodPost, "/v1/spaces/s1/drawings/d1/collaboration-ticket", true},
		{http.MethodPost, "/v1/spaces/s1/conversations/c1/read", true},
		{http.MethodPost, "/v1/spaces/s1/nodes/n1/resolve", true},
		{http.MethodPost, "/v1/devices/dev1/heartbeat", true},
		{http.MethodPost, "/v1/spaces", false},
		{http.MethodPost, "/v1/devices", false},
		{http.MethodPost, "/v1/ai/invocations", false},
		{http.MethodPost, "/v1/spaces/s1/library/uploads", false},
		{http.MethodPost, "/v1/me/export/extra", false},
		{http.MethodPost, "/v1/setup-keys/redeem", true},
		{http.MethodPut, "/v1/me/password", true},
		{http.MethodPut, "/v1/me/settings", false},
		{http.MethodPost, "/v1/auth/forgot", false},
		{http.MethodPatch, "/v1/spaces/s1/notes/n1", false},
		{http.MethodDelete, "/v1/spaces/s1/messages/m1", false},
	} {
		if got := readOnlyRequest(httptest.NewRequest(tc.method, tc.path, nil)); got != tc.allowed {
			t.Errorf("%s %s read-only = %v, want %v", tc.method, tc.path, got, tc.allowed)
		}
	}
}

func TestClosingAccountsOpenCollaborationRoomsAsViewers(t *testing.T) {
	previous := suspensions
	t.Cleanup(func() { suspensions = previous })
	NewSuspensionGuard(fakeAccountStates{closing: map[string]time.Time{"closing": time.Now().Add(time.Hour)}})
	if role := collaborationRole("closing", "creator"); role != "viewer" {
		t.Fatalf("closing account role = %s", role)
	}
	if role := collaborationRole("active", "editor"); role != "editor" {
		t.Fatalf("active account role = %s", role)
	}
}

func TestAccountClosingMessageNamesTheDateAndHowToKeepIt(t *testing.T) {
	message := AccountClosingMessage(time.Date(2026, 12, 1, 15, 0, 0, 0, time.UTC))
	for _, want := range []string{"read-only", "export", "December 1, 2026", "Subscribe again"} {
		if !strings.Contains(message, want) {
			t.Fatalf("message %q lacks %q", message, want)
		}
	}
}

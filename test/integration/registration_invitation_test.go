package integration

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	api "github.com/misty-org/misty-server/internal/platform/httpapi"
	"github.com/misty-org/misty-server/internal/platform/security"
)

// A production server takes its first account, then only invited ones.
func TestProductionRegistrationIsFirstAccountThenInvitations(t *testing.T) {
	database := openIntegrationDatabase(t)
	t.Setenv("MISTY_ENVIRONMENT", "production")
	t.Setenv("MISTY_AUTH_SIGNING_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	register := func(username, email string) int {
		return performJSONRequest(t, api.Register(database), http.MethodPost, "/register", map[string]string{
			"username": username, "email": email, "password": "correct horse battery staple",
		}).Code
	}
	if code := register("owner", "owner@example.test"); code != http.StatusCreated {
		t.Fatalf("first account = %d", code)
	}
	if code := register("stranger", "stranger@example.test"); code != http.StatusForbidden {
		t.Fatalf("uninvited account = %d, want 403", code)
	}
	owner, _, err := database.GetUserByEmail("owner@example.test")
	if err != nil || owner == nil {
		t.Fatalf("owner = %v, %v", owner, err)
	}
	space, err := database.TestingCreateSpace(t.Context(), owner.ID, "Team")
	if err != nil {
		t.Fatal(err)
	}
	token, _ := security.GenerateSecureToken()
	if _, err := database.InviteToSpaceWithToken(t.Context(), owner.ID, space.ID, "Friend@example.test", security.HashToken(token)); err != nil {
		t.Fatal(err)
	}
	if code := register("friend", "friend@example.test"); code != http.StatusCreated {
		t.Fatalf("invited account = %d, want 201", code)
	}
}

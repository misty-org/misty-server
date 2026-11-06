package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	. "github.com/misty-org/misty-server/internal/platform/httpapi"
	"github.com/misty-org/misty-server/test/testkit"
)

func TestInviteLinkNamesItsOwnerUntilTheAccountCloses(t *testing.T) {
	database := testkit.OpenDatabase(t)
	user, err := database.CreateUser("inviter-"+uuid.NewString()+"@example.com", strongPassword)
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Get("/v1/me/invite-code", MyInviteCode(database))
	router.Get("/v1/invites/{code}", InviteByCode(database))

	if rec, _ := call(t, router, http.MethodGet, "/v1/me/invite-code", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("signed out = %d", rec.Code)
	}
	if rec, body := call(t, router, http.MethodGet, "/v1/invites/nosuchcode", "", ""); rec.Code != http.StatusNotFound || body["code"] != "invite_not_found" {
		t.Fatalf("unknown code = %d %v", rec.Code, body)
	}

	ctx := context.Background()
	code, err := database.InviteCode(ctx, user.ID)
	if err != nil || len(code) != 10 {
		t.Fatalf("code = %q, %v", code, err)
	}
	again, err := database.InviteCode(ctx, user.ID)
	if err != nil || again != code {
		t.Fatalf("second code = %q, %v; want %q", again, err, code)
	}

	rec, body := call(t, router, http.MethodGet, "/v1/invites/"+code, "", "")
	if rec.Code != http.StatusOK || body["name"] != user.Username || len(body) != 1 {
		t.Fatalf("lookup = %d %v; want only the username %q", rec.Code, body, user.Username)
	}

	if err := database.SuspendAccount(ctx, user.ID); err != nil {
		t.Fatal(err)
	}
	if rec, _ := call(t, router, http.MethodGet, "/v1/invites/"+code, "", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("suspended account's link = %d", rec.Code)
	}
}

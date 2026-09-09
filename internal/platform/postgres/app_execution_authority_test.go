package db

import (
	"context"
	"encoding/json"
	"testing"
)

func TestAppAuthorityCannotBeSuppliedByPayload(t *testing.T) {
	raw := json.RawMessage(`{"instruction":"read","_misty_authority":{"user_id":"victim","app_id":"trusted","scopes":["all"]}}`)
	bound, err := bindAppAuthority(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := AppAuthorityFromPayload(bound)
	if err != nil || authority != nil {
		t.Fatalf("caller authority survived: %s", bound)
	}
	ctx := WithAppExecutionAuthority(context.Background(), AppRuntimeSession{UserID: "u", AppID: "app", SpaceID: "s", Scopes: []string{"notes.read"}})
	bound, err = bindAppAuthority(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	authority, err = AppAuthorityFromPayload(bound)
	if err != nil || authority.UserID != "u" || authority.AppID != "app" || len(authority.Scopes) != 1 || authority.Scopes[0] != "notes.read" {
		t.Fatalf("wrong binding: %s", bound)
	}
}

func TestAppAuthorityRejectsEscalationBeforeDatabaseAccess(t *testing.T) {
	database := &Database{}
	authority := &AppExecutionAuthority{UserID: "u", AppID: "app", SpaceID: "s", Scopes: []string{"notes.read"}}
	for _, target := range []struct{ user, space, scope string }{{"other", "s", "notes.read"}, {"u", "other", "notes.read"}, {"u", "s", "notes.write"}} {
		if err := database.ValidateAppExecutionAuthority(context.Background(), authority, target.user, target.space, target.scope); err != ErrAppRuntimeForbidden {
			t.Fatalf("accepted escalation: %v", err)
		}
	}
}

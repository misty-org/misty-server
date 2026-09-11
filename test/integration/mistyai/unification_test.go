package mistyai_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/test/testkit"
)

func TestPrivateActivityDelegationAndRevocation(t *testing.T) {
	database := testkit.OpenDatabase(t)
	ctx := context.Background()
	owner, err := database.CreateUser("Misty owner", "misty-owner@example.test", "password123")
	if err != nil {
		t.Fatal(err)
	}
	other, err := database.CreateUser("Other", "misty-other@example.test", "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(ctx, owner.ID, "Misty test")
	if err != nil {
		t.Fatal(err)
	}
	spec := db.AppInstallSpec{ID: "agents", Version: "1", PermissionVersion: 1, Scopes: []string{"ai.use"}}
	if _, err = database.InstallSpaceApp(ctx, owner.ID, space.ID, spec, nil); err != nil {
		t.Fatal(err)
	}
	identity, err := database.EnsureAskIdentity(ctx, owner.ID, "gpt-5")
	if err != nil {
		t.Fatal(err)
	}
	invocation, _, err := database.CreateAIInvocationRecord(ctx, db.AIInvocationRecord{ID: "invocation_misty_test", UserID: owner.ID, SpaceID: space.ID, SurfaceID: "global", Mode: "drawer", Trigger: "message", State: "running", IdempotencyKey: "misty-test", RequestPayload: json.RawMessage(`{"prompt":"Private research"}`), ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	input := db.CreatorAgentRunInput{Instruction: "Independent research", Mode: "auto", ParentInvocationID: invocation.ID}
	child, err := database.CreateCreatorAgentRun(ctx, owner.ID, space.ID, identity.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err = database.CreateCreatorAgentRun(ctx, owner.ID, space.ID, identity.ID, input); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = database.CreateCreatorAgentRun(ctx, owner.ID, space.ID, identity.ID, input); err == nil {
		t.Fatal("delegation limit not enforced")
	}
	activity, err := database.MistyActivity(ctx, owner.ID, space.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(activity), child.ID) || !strings.Contains(string(activity), "Private research") {
		t.Fatalf("missing activity: %s", activity)
	}
	if _, err = database.MistyActivity(ctx, other.ID, space.ID); err == nil {
		t.Fatal("private activity exposed")
	}
	if err = database.CancelMistyInvocationChildren(ctx, owner.ID, invocation.ID); err != nil {
		t.Fatal(err)
	}
	activity, err = database.MistyActivity(ctx, owner.ID, space.ID)
	if err != nil {
		t.Fatal(err)
	}
	var entries []struct {
		State string `json:"state"`
	}
	if err = json.Unmarshal(activity, &entries); err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.State != "canceled" {
			t.Fatalf("descendant not canceled: %s", activity)
		}
	}
	if _, err = database.RemoveSpaceApp(ctx, owner.ID, space.ID, "agents"); err != nil {
		t.Fatal(err)
	}
	if err = database.RequireSpaceApp(ctx, owner.ID, space.ID, "agents"); err == nil {
		t.Fatal("removed Agents remains available")
	}
	if _, err = database.MistyActivity(ctx, owner.ID, space.ID); err == nil {
		t.Fatal("dashboard survived revocation")
	}
}

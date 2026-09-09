package db

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	serveragent "github.com/kannachi323/misty/server/internal/agents"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestSDKPlannerResolutionRequiresOriginAndPinsItsSpace(t *testing.T) {
	database := openTestDatabase(t)
	ctx := t.Context()
	user, err := database.CreateUser("Planner pilot", uuid.NewString()+"@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	first := createTestSpace(t, database, ctx, user.ID, "First")
	second := createTestSpace(t, database, ctx, user.ID, "Second")
	if _, err := database.InstallUserApp(ctx, user.ID, "planner", "1.1.0", 6, []string{"tasks.read", "tasks.write"}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ResolveSDKTargets(ctx, user.ID, cap.TargetResolve{Capability: "tasks.create"}); !errors.Is(err, ErrSDKTargetClarification) {
		t.Fatalf("missing Space selected an arbitrary Planner: %v", err)
	}
	targets, err := database.ResolveSDKTargets(ctx, user.ID, cap.TargetResolve{Capability: "tasks.create", SpaceID: first.ID})
	if err != nil || len(targets) != 1 || targets[0].SpaceID != first.ID || targets[0].Label != "First · Planner" {
		t.Fatalf("first Space: %#v %v", targets, err)
	}
	firstTarget := targets[0]
	explicit, err := database.ResolveSDKTargets(ctx, user.ID, cap.TargetResolve{Capability: "tasks.create", SpaceID: first.ID, ProviderID: "example.todoist/browser"})
	if err != nil || len(explicit) != 0 {
		t.Fatalf("substituted Planner for requested provider: %#v %v", explicit, err)
	}
	agent, err := database.EnsureAskIdentity(ctx, user.ID, serveragent.InitialSelectedModelID)
	if err != nil {
		t.Fatal(err)
	}
	run, err := database.CreateCreatorAgentRun(ctx, user.ID, first.ID, agent.ID, CreatorAgentRunInput{Instruction: "Create a follow-up task"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ResolveSDKTargets(ctx, user.ID, cap.TargetResolve{Capability: "tasks.create", SpaceID: second.ID}); err != nil {
		t.Fatal(err)
	}
	pins, err := database.AgentSDKCapabilityBindings(ctx, user.ID, run.ID)
	if err != nil || len(pins) != 1 || pins[0].TargetID != firstTarget.ID || pins[0].AdapterVersion != "sdk-planner-v1" {
		t.Fatalf("Space switch retargeted run: %#v %v", pins, err)
	}
	bound, err := database.ResolveAgentSDKCapability(ctx, user.ID, run.ID, pins[0])
	if err != nil || bound.Target.SpaceID != first.ID {
		t.Fatalf("pinned Planner unavailable: %#v %v", bound, err)
	}
	other, err := database.CreateUser("Other", uuid.NewString()+"@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ResolveSDKTargets(ctx, other.ID, cap.TargetResolve{Capability: "tasks.create", SpaceID: first.ID}); !errors.Is(err, ErrSpaceForbidden) {
		t.Fatalf("foreign Space resolved: %v", err)
	}
	if err := database.RevokeSDKTarget(ctx, user.ID, firstTarget.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ResolveAgentSDKCapability(ctx, user.ID, run.ID, pins[0]); !errors.Is(err, ErrSDKProviderUnavailable) {
		t.Fatalf("revoked Planner still executable: %v", err)
	}
}

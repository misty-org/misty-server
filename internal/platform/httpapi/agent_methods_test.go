package api

import (
	"errors"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/test/testkit"
	"os"
	"testing"
	"time"
)

func TestAgentMethodRuntimePinsAndTargetBoundaries(t *testing.T) {
	if os.Getenv("TEST_DB_NAME") != "misty_agent_phases34_test" {
		t.Skip("requires isolated phase3/4 database")
	}
	database := testkit.OpenDatabase(t)
	ctx := t.Context()
	owner, err := database.CreateUser("Methods", "methods@example.test", "password123")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := database.SavePersonalAgent(ctx, owner.ID, "", db.AgentProfileInput{Name: "Methods", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	m, err := database.SaveAgentMethod(ctx, owner.ID, db.AgentMethod{AgentID: agent.ID, Kind: "workflow", Enabled: true, Definition: db.AgentMethodDefinition{Title: "Read browser", Instructions: "Read the assigned browser only.", Target: "separate_window", RequiredTools: []string{"browser.inspect"}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	body := aiInvocationInput{AgentID: agent.ID, MethodVersionID: m.VersionID, Prompt: "Forged instructions"}
	if _, err := resolveInvocationMethod(ctx, database, owner.ID, &body); !errors.Is(err, db.ErrSpaceConflict) {
		t.Fatal("missing device did not fail", err)
	}
	body.ExecutionMode = "team"
	body.WindowLabel = "main"
	body.TaskID = "task"
	body.DeviceContexts = []aiInvocationDeviceContext{{}}
	if _, err := resolveInvocationMethod(ctx, database, owner.ID, &body); err == nil {
		t.Fatal("separate workflow accepted main")
	}
	body.WindowLabel = "misty-agent-fixture"
	if _, err := resolveInvocationMethod(ctx, database, owner.ID, &body); err != nil || body.Prompt != m.Definition.Instructions {
		t.Fatal("pin not resolved", err)
	}
	if _, err := invocationMethodGuidance(ctx, database, owner.ID, &body, nil); err == nil {
		t.Fatal("missing required tool")
	}
	if _, err := invocationMethodGuidance(ctx, database, owner.ID, &body, []string{"browser.inspect"}); err != nil {
		t.Fatal(err)
	}
	skill, err := database.SaveAgentMethod(ctx, owner.ID, db.AgentMethod{AgentID: agent.ID, Kind: "skill", Enabled: true, Definition: db.AgentMethodDefinition{Title: "Writing", Instructions: "Write briefly.", Target: "cloud"}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := pinInvocationSkills(ctx, database, owner.ID, &body); err != nil || len(body.SkillVersionIDs) != 1 || body.SkillVersionIDs[0] != skill.VersionID {
		t.Fatal("skill not pinned", err)
	}
	old := skill.VersionID
	skill.Definition.Instructions = "Use paragraphs."
	skill, err = database.SaveAgentMethod(ctx, owner.ID, skill, skill.Version)
	if err != nil {
		t.Fatal(err)
	}
	if body.SkillVersionIDs[0] != old {
		t.Fatal("run changed skill version")
	}
	skill.Enabled = false
	if _, err = database.SaveAgentMethod(ctx, owner.ID, skill, skill.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := invocationMethodGuidance(ctx, database, owner.ID, &body, []string{"browser.inspect"}); err == nil {
		t.Fatal("revoked skill remained usable")
	}
	// A device-dependent workflow can be scheduled, but a scheduled run cannot become cloud work.
	timing := db.ScheduleTiming{Timezone: time.UTC.String(), Rules: []db.ScheduleRule{{Frequency: "daily", Times: []string{"09:00"}}}}
	if _, err := saveWorkflowSchedule(ctx, database, owner.ID, m.ID, workflowScheduleInput{ScheduleTiming: timing}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	scheduledBody := aiInvocationInput{AgentID: agent.ID, MethodVersionID: m.VersionID}
	if _, err := resolveInvocationMethod(ctx, database, owner.ID, &scheduledBody); !errors.Is(err, db.ErrSpaceConflict) {
		t.Fatal("scheduled device method silently fell back", err)
	}
}

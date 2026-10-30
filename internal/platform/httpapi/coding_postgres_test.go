package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/platform/db"
	"github.com/kannachi323/misty/server/test/testkit"
)

func TestCodingDelegationPostgres(t *testing.T) {
	database := testkit.OpenDatabase(t)
	ctx := t.Context()
	owner, _ := database.CreateUser("coding-owner@example.test", "password123")
	device := "device_" + uuid.NewString()
	// The device that holds the project was last seen an hour ago: offline.
	if err := database.SpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO trusted_devices(id,user_id,name,public_key,last_seen_at) VALUES($1,$2,'Studio Mac',repeat('k',44),now()-interval '1 hour')`, device, owner.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	service := &SpacesService{database: database, aiInvocations: newAIInvocationHub(database)}
	body := aiInvocationInput{Mode: "drawer", SurfaceID: "global", Trigger: "message", Prompt: "Fix the bug", Timezone: "UTC", PermissionMode: "ask"}
	payload, _ := json.Marshal(body)
	parent, _, err := database.CreateAIInvocationRecord(ctx, db.AIInvocationRecord{
		ID: "invocation_" + uuid.NewString(), UserID: owner.ID, SurfaceID: "global", Mode: "drawer", Trigger: "message", State: "queued",
		IdempotencyKey: uuid.NewString(), RequestPayload: payload, ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	projects := []deviceGrant{{scopeID: "scope_app", name: "app", deviceID: device, deviceName: "Studio Mac"}}

	// An offline device: the child run fails with a plain reason and the model
	// is told to wait for the device.
	raw, err := service.delegateCoding(ctx, &parent, body, projects, collaborationState{mode: db.ConversationModeAct}, agent.ToolRequest{ID: "call-1", Arguments: json.RawMessage(`{"task":"Fix the parser"}`)})
	if err != nil {
		t.Fatal(err)
	}
	var result struct{ Status, Device, UserMessage string }
	_ = json.Unmarshal(raw, &result)
	if result.Status != "device_offline" || result.Device != "Studio Mac" || !strings.Contains(string(raw), "Waiting for Studio Mac") {
		t.Fatalf("offline delegate = %s", raw)
	}
	children, err := database.DelegationChildren(ctx, owner.ID, parent.ID, "call-1")
	if err != nil || len(children) != 1 {
		t.Fatalf("children = %+v, %v", children, err)
	}
	child, err := database.AIInvocationByID(ctx, owner.ID, children[0].ChildInvocationID)
	if err != nil || child.State != "failed" {
		t.Fatalf("child = %+v, %v", child, err)
	}
	var childBody aiInvocationInput
	_ = json.Unmarshal(child.RequestPayload, &childBody)
	if childBody.ExecutionMode != codingExecutionMode || childBody.DelegatedFrom != parent.ID || childBody.PermissionMode != "ask" || childBody.CodingProject != "scope_app" {
		t.Fatalf("child body = %+v", childBody)
	}

	// A run that reaches its device mirrors the tool's work: text, its todo
	// list as the child's checklist, and the end. Usage reports are ignored.
	parent.ConversationID = ""
	liveID, err := service.startCodingChild(ctx, &parent, body, "call-2", "Fix the parser", "edit", "scope_app")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.InsertDelegationChild(ctx, owner.ID, parent.ID, db.DelegationChild{CallID: "call-2", Goal: "Fix the parser", ChildInvocationID: liveID}); err != nil {
		t.Fatal(err)
	}
	live, err := service.codingRun(ctx, owner.ID, liveID)
	if err != nil {
		t.Fatal(err)
	}
	liveLease, _, err := database.IssueCodingLease(ctx, owner.ID, db.CodingLease{InvocationID: liveID, DeviceID: device, Engine: "claude", TokenBudget: 5000, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	var mirrored []codingEvent
	_ = json.Unmarshal([]byte(`[{"seq":1,"type":"delta","text":"Fixed "},{"seq":2,"type":"todos","items":[{"id":"t1","title":"Edit parser","status":"in_progress"}]},
		{"seq":3,"type":"usage","input":100,"output":50,"cache_read":10},{"seq":4,"type":"usage","input":300,"output":100,"cache_write":40,"cache_read":1000},
		{"seq":5,"type":"message","text":"Fixed the parser."},{"seq":6,"type":"done","stop_reason":"success"}]`), &mirrored)
	for _, event := range mirrored {
		if err := service.recordCodingEvent(ctx, live, liveLease, event); err != nil {
			t.Fatalf("%s: %v", event.Type, err)
		}
	}
	finished, _ := database.DelegationChildren(ctx, owner.ID, parent.ID, "call-2")
	if len(finished) != 1 || finished[0].State != "completed" || finished[0].Answer != "Fixed the parser." {
		t.Fatalf("delegated run = %+v", finished)
	}
	// A project grant needs the project attached from the person's own device.
	if err := service.validateCodingScope(ctx, owner.ID, agent.ScopeProject, "scope_app"); err == nil {
		t.Fatal("a project never attached to a chat can't be remembered")
	}

	// Leases: a wrong token is refused.
	lease, token, err := database.IssueCodingLease(ctx, owner.ID, db.CodingLease{InvocationID: child.ID, DeviceID: device, Engine: "claude", TokenBudget: 1000, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CodingLeaseByToken(ctx, owner.ID, child.ID, lease.ID, "wrong"); !errors.Is(err, db.ErrSpaceForbidden) {
		t.Fatalf("wrong token = %v", err)
	}
	if found, err := database.CodingLeaseByToken(ctx, owner.ID, child.ID, lease.ID, token); err != nil || found.TokenBudget != 1000 {
		t.Fatalf("lease = %+v, %v", found, err)
	}
	// Engine reports: installed tools get their own hidden managed agent.
	if err := database.SaveCodingEngines(ctx, owner.ID, device, []db.CodingEngine{{Engine: "codex", Installed: true, SignedIn: true, Version: "1.0"}}); err != nil {
		t.Fatal(err)
	}
	first, err := database.EnsureEngineAgent(ctx, owner.ID, "codex")
	second, _ := database.EnsureEngineAgent(ctx, owner.ID, "codex")
	if err != nil || first == "" || first != second {
		t.Fatalf("engine agent = %q %q, %v", first, second, err)
	}
	agents, _ := database.PersonalAgents(ctx, owner.ID)
	for _, item := range agents {
		if item.ID == first {
			t.Fatal("engine agents stay out of the agent switcher")
		}
	}
	if err := database.SaveCodingEngines(ctx, owner.ID, "device_"+uuid.NewString(), []db.CodingEngine{{Engine: "codex"}}); !errors.Is(err, db.ErrDeviceNotFound) {
		t.Fatalf("an unknown device = %v", err)
	}
}

func TestCodingEngineRules(t *testing.T) {
	run := func(mode, collaboration string) *codingRun {
		return &codingRun{body: aiInvocationInput{PermissionMode: mode, CollaborationMode: collaboration}}
	}
	all := []string{"project.edit", "project.install", "git.push"}
	if codingEngineRefusal("claude", run("ask", "act"), nil) != "" {
		t.Fatal("tools that ask run in Ask")
	}
	if !strings.Contains(codingEngineRefusal("grok", run("auto", "act"), all[:2]), "Grok can't ask before acting") {
		t.Fatal("Grok needs every project grant")
	}
	if codingEngineRefusal("grok", run("auto", "act"), all) != "" {
		t.Fatal("Grok runs in Auto with every grant")
	}
	if codingEngineRefusal("grok", run("ask", "act"), all) == "" || codingEngineRefusal("grok", run("auto", "plan"), all) == "" {
		t.Fatal("Grok never runs in Ask or Plan")
	}
	if !strings.Contains(codingEngineRefusal("dsh", run("auto", "act"), all[:1]), "DeepSeek Shell can't ask") || codingEngineRefusal("dsh", run("auto", "act"), all) != "" {
		t.Fatal("DeepSeek Shell runs like Grok: Auto, with every project grant")
	}
	if codingEngineRefusal("agy", run("ask", "act"), nil) == "" || codingEngineRefusal("agy", run("ask", "plan"), nil) != "" {
		t.Fatal("launch-time tools only plan in Ask")
	}
}

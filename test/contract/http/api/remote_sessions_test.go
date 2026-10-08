package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	serveragent "github.com/kannachi323/misty/server/internal/agents"
	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// Agents on devices (docs/design/agents-on-devices/BRIEF.md): a request made
// on one device opens a browser session on another it picked. The other
// device takes the job through the signed claim, opens the session and signs
// its own tab; the server accepts nothing it could have forged.

type remoteSessionFixture struct {
	*deviceFixture
	asking, picked testDevice
	service        *SpacesService
	runID          string
	agentID        string
}

func newRemoteSessionFixture(t *testing.T) *remoteSessionFixture {
	f := newDeviceFixture(t)
	asking := f.register("Laptop")
	picked := f.register("Mac mini")
	for version, admitted := range [][]testDevice{{asking}, {asking, picked}} {
		device := admitted[len(admitted)-1]
		if response := f.admit(device, int64(version+1), admitted, f.root); response.Code != http.StatusOK {
			t.Fatalf("admit: %d %s", response.Code, response.Body.String())
		}
	}
	// The Mac mini's own policy lets agents use its browser.
	if response := f.signed(picked, http.MethodPut, "/devices/"+picked.id+"/policy", map[string]any{"policy": policyRecord(picked.key, f.userID, picked.id, 1, "view")}); response.Code != http.StatusOK {
		t.Fatalf("policy: %d %s", response.Code, response.Body.String())
	}
	agents := NewAgentsService(f.database)
	for _, action := range []string{"begin", "complete", "fail"} {
		f.router.Post("/devices/{deviceID}/workflow-node-jobs/{jobID}/"+action, agents.DeviceAuthenticated(agents.WorkflowNodeLeaseAction(action)))
	}
	service, err := NewSpacesService(f.database, nil, base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	if err != nil {
		t.Fatal(err)
	}
	agent, err := f.database.EnsureAskIdentity(t.Context(), f.userID, serveragent.InitialSelectedModelID)
	if err != nil {
		t.Fatal(err)
	}
	// A desktop chat: the person's agent, in user mode, from the main window.
	run := f.request(t, agent.ID)
	// Attached at dispatch, as for any request. The laptop picked the Mac mini: its browser grant may open a session.
	if _, err := f.database.AttachAIInvocationContext(t.Context(), f.userID, run.ID, "", picked.id, "workspace", "workspace:"+picked.id, "Misty browser on Mac mini",
		json.RawMessage(`["tabs.list","tabs.open","browser.session"]`), json.RawMessage(`{"source":"device"}`)); err != nil {
		t.Fatal(err)
	}
	activate(t, f.database, run.ID)
	return &remoteSessionFixture{deviceFixture: f, asking: asking, picked: picked, service: service, runID: run.ID, agentID: agent.ID}
}

func (f *deviceFixture) request(t *testing.T, agentID string) db.AIInvocationRecord {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{"agent_id": agentID, "execution_mode": "user", "window_label": "main"})
	run, _, err := f.database.CreateAIInvocationRecord(t.Context(), db.AIInvocationRecord{ID: "invocation_" + uuid.NewString(), UserID: f.userID, SurfaceID: "global", Mode: "drawer", Trigger: "message", State: "queued",
		IdempotencyKey: uuid.NewString(), RequestPayload: payload, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func activate(t *testing.T, database *db.Database, runID string) {
	t.Helper()
	if _, err := database.ActivateAIInvocationRuntime(t.Context(), runID, "vercel-workflow", uuid.NewString()); err != nil {
		t.Fatal(err)
	}
}

// claimable queues one job for the picked device and reports whether its
// claim admits it (rather than canceling it).
func (f *remoteSessionFixture) claimable(t *testing.T, runID, scope, operation string) bool {
	t.Helper()
	if _, err := f.database.QueueAIInvocationDeviceNodeJob(t.Context(), f.userID, runID, "node_"+uuid.NewString(), 1, scope, operation, operation,
		json.RawMessage(`{}`), json.RawMessage(`{}`), json.RawMessage(`{"type":"object"}`), json.RawMessage(`{"type":"object"}`)); err != nil {
		t.Fatalf("queue %s: %v", operation, err)
	}
	response := f.signed(f.picked, http.MethodPost, "/devices/"+f.picked.id+"/workflow-node-jobs/claim", map[string]any{"protocolVersion": 2})
	return response.Code == http.StatusOK
}

// answer plays the picked device: it claims the session job and finishes it
// with the given output, or fails it with errorCode.
func (f *remoteSessionFixture) answer(t *testing.T, output map[string]any, errorCode string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		response := f.signed(f.picked, http.MethodPost, "/devices/"+f.picked.id+"/workflow-node-jobs/claim", map[string]any{"protocolVersion": 2})
		if response.Code == http.StatusNoContent {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		var claim struct {
			Job struct {
				ID        string `json:"id"`
				Operation string `json:"operation"`
			} `json:"job"`
			LeaseToken string `json:"leaseToken"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &claim) != nil || claim.Job.Operation != "browser.session" {
			t.Errorf("claim: %d %s", response.Code, response.Body.String())
			return
		}
		path := "/devices/" + f.picked.id + "/workflow-node-jobs/" + claim.Job.ID
		if response := f.signed(f.picked, http.MethodPost, path+"/begin", map[string]any{"leaseToken": claim.LeaseToken}); response.Code != http.StatusOK {
			t.Errorf("begin: %d %s", response.Code, response.Body.String())
			return
		}
		if errorCode != "" {
			f.signed(f.picked, http.MethodPost, path+"/fail", map[string]any{"leaseToken": claim.LeaseToken, "errorCode": errorCode})
			return
		}
		if response := f.signed(f.picked, http.MethodPost, path+"/complete", map[string]any{"leaseToken": claim.LeaseToken, "output": output}); response.Code != http.StatusOK {
			t.Errorf("complete: %d %s", response.Code, response.Body.String())
		}
		return
	}
	t.Error("the session job never reached the picked device")
}

func (f *remoteSessionFixture) tab(signer testDevice, requester string) map[string]any {
	capabilities := []string{"browser.snapshot", "browser.steps"}
	return map[string]any{"tabs": []map[string]any{{
		"device_id": f.picked.id, "kind": "browser_tab", "opaque_ref": "agent-scope-1", "display_name": "Browser on Mac mini",
		"capabilities": capabilities, "metadata": map[string]any{"app_id": "browser", "remote_session": true, "task_id": "hosted-task"},
		"run_grant": runGrant(signer.key, f.userID, requester, f.picked.id, capabilities, []string{"agent-scope-1"}),
	}}}
}

func (f *remoteSessionFixture) open(t *testing.T, call string) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	return f.service.TestingOpenRemoteSession(ctx, f.userID, f.runID, call, "https://forms.example/apply")
}

func TestRemoteSessionOpensOnThePickedDevice(t *testing.T) {
	f := newRemoteSessionFixture(t)
	go f.answer(t, f.tab(f.picked, f.picked.id), "")
	result, err := f.open(t, "call-1")
	if err != nil {
		var state, code string
		_ = f.database.Conn.QueryRow(`SELECT state,COALESCE(error_code,'') FROM workflow_device_node_jobs WHERE invocation_id=$1`, f.runID).Scan(&state, &code)
		t.Fatalf("open: %v (job %s %s)", err, state, code)
	}
	var reply struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	if json.Unmarshal(result, &reply) != nil || reply.Status != "screen_requested" || !strings.Contains(reply.Message, "Mac mini") {
		t.Fatalf("reply: %s", result)
	}

	// The laptop continues with the session's tab: the agent may act there,
	// in a user-mode chat, though the tab has no window on the laptop.
	next := f.request(t, f.agentID)
	attach := func(scope string) {
		capabilities := json.RawMessage(`["browser.snapshot","browser.steps"]`)
		if _, err := f.database.AttachAIInvocationContext(t.Context(), f.userID, next.ID, "", f.picked.id, "browser_tab", scope, "Browser on Mac mini", capabilities,
			json.RawMessage(`{"app_id":"browser","remote_session":true,"task_id":"hosted-task"}`)); err != nil {
			t.Fatal(err)
		}
	}
	attach("agent-scope-1")
	// A tab no session opened, claiming to be one.
	attach("agent-scope-forged")
	activate(t, f.database, next.ID)
	if !f.claimable(t, next.ID, "agent-scope-1", "browser.steps") {
		t.Fatal("the session's own tab could not be acted on")
	}
	if f.claimable(t, next.ID, "agent-scope-forged", "browser.steps") {
		t.Fatal("a tab no session opened was acted on in a user-mode chat")
	}
}

func TestSharedFoldersWorkFromAnAgentChat(t *testing.T) {
	f := newRemoteSessionFixture(t)
	// Folders carry no window: a chat from the main window may still read one.
	next := f.request(t, f.agentID)
	if _, err := f.database.AttachAIInvocationContext(t.Context(), f.userID, next.ID, "", f.picked.id, "local_folder", "scope-1", "Reports",
		json.RawMessage(`["files.list","files.read"]`), json.RawMessage(`{"source":"device"}`)); err != nil {
		t.Fatal(err)
	}
	activate(t, f.database, next.ID)
	if !f.claimable(t, next.ID, "scope-1", "files.read") {
		t.Fatal("a shared folder could not be read from a user-mode agent chat")
	}
}

func TestRemoteSessionRefusesATabTheDeviceDidNotSign(t *testing.T) {
	f := newRemoteSessionFixture(t)
	// The laptop's key signing for the Mac mini's own tab: a forgery.
	go f.answer(t, f.tab(f.asking, f.picked.id), "")
	if _, err := f.open(t, "call-forged"); err == nil {
		t.Fatal("a tab signed by another device's key was accepted")
	}
}

func TestRemoteSessionTellsThePersonToPickAnotherDevice(t *testing.T) {
	f := newRemoteSessionFixture(t)
	// Busy: the agent is already working there.
	go f.answer(t, nil, "agent_busy")
	if _, err := f.open(t, "call-busy"); err == nil || !strings.Contains(err.Error(), "device_offline: Mac mini is busy") {
		t.Fatalf("busy device: %v", err)
	}
	// Silent: it never takes the job.
	restore := TestingSetRemoteSessionWait(300 * time.Millisecond)
	defer restore()
	if _, err := f.open(t, "call-silent"); err == nil || !strings.Contains(err.Error(), "device_offline: Mac mini did not answer. Pick another device") {
		t.Fatalf("silent device: %v", err)
	}
	// Offline: not seen recently, so nothing is queued.
	if _, err := f.database.Conn.Exec(`UPDATE trusted_devices SET last_seen_at=NOW()-INTERVAL '10 minutes' WHERE id=$1`, f.picked.id); err != nil {
		t.Fatal(err)
	}
	if _, err := f.open(t, "call-offline"); err == nil || !strings.Contains(err.Error(), "device_offline: Mac mini is offline") {
		t.Fatalf("offline device: %v", err)
	}
}

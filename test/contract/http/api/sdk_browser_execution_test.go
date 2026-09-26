package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/kannachi323/misty/server/internal/agenttools"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/browseractions"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// These tests cross actual admission, runtime MCP dispatch, encrypted reviews,
// effect journaling and leased device jobs. Only the device's website is a fixture.
func TestSDKBrowserAndPlannerInvocationGate(t *testing.T) {
	for _, scenario := range []string{"gmail", "outlook", "todoist", "planner", "gmail/lost", "gmail/stale", "gmail/account", "gmail/recipients", "gmail/content", "gmail/cancel", "gmail/disconnected", "gmail/unconfirmed", "todoist/destination", "gmail/global", "todoist/global", "planner/global", "gmail/read", "outlook/read", "gmail/draft", "outlook/draft", "gmail/login"} {
		t.Run(scenario, func(t *testing.T) {
			name, mode, _ := strings.Cut(scenario, "/")
			ctx, cancelTest := context.WithTimeout(t.Context(), 45*time.Second)
			defer cancelTest()
			t.Setenv("MISTY_SDK_EXECUTION_ENABLED", "true")
			database := openPresenceTestDatabase(t)
			user, err := database.CreateUser("Pilot", uniqueTestEmail("pilot"), "password123")
			if err != nil {
				t.Fatal(err)
			}
			space, err := database.CreateSpace(ctx, user.ID, "Pilot Space")
			if err != nil {
				t.Fatal(err)
			}
			var target *cap.Target
			var device *db.TrustedDevice
			origin := "https://mail.google.com"
			if name == "outlook" {
				origin = "https://outlook.live.com"
			}
			if name == "todoist" {
				origin = "https://app.todoist.com"
			}
			capability := "inbox.send"
			if mode == "read" {
				capability = "inbox.read"
			}
			if mode == "draft" {
				capability = "inbox.draft"
			}
			if name == "todoist" || name == "planner" {
				capability = "tasks.create"
			}
			var binding cap.BrowserBinding
			if name == "planner" {
				targets, err := database.ResolveSDKTargets(ctx, user.ID, cap.TargetResolve{Capability: "tasks.create", SpaceID: space.ID})
				if err != nil || len(targets) != 1 {
					t.Fatalf("Planner default: %v %#v", err, targets)
				}
				target = &targets[0]
			} else {
				providerID := "inbox/" + name
				if name == "todoist" {
					providerID = "planner/todoist"
				}
				provider, ok := cap.OfficialBrowserProvider(providerID, 1)
				if !ok {
					t.Fatal("missing first-party provider")
				}
				deviceKey, _, _ := ed25519.GenerateKey(rand.Reader)
				device, err = database.RegisterTrustedDevice(user.ID, "Pilot Mac", base64.RawURLEncoding.EncodeToString(deviceKey), "macos", "", json.RawMessage(`[]`), json.RawMessage(`{"browser_tools":true}`))
				if err != nil {
					t.Fatal(err)
				}
				binding = cap.BrowserBinding{Kind: "browser", DeviceID: device.ID, ProfileID: strings.Repeat("a", 64), AccountBindingID: uuid.NewString(), Origins: []string{origin}, ScopeID: "browser-pilot", AccountIdentity: "pilot@example.com"}
				target, err = database.ConfigureSDKTarget(ctx, user.ID, cap.TargetConfiguration{TargetID: uuid.NewString(), ProviderID: provider.ID, ProviderVersion: 1, SpaceID: space.ID, Label: "Pilot account", Capabilities: []string{capability}, CallerApps: []string{}, Browser: &binding})
				if err != nil {
					t.Fatal(err)
				}
			}
			if name == "todoist" && mode == "" {
				duplicate := binding
				duplicate.ScopeID = "browser-second"
				duplicate.AccountBindingID = uuid.NewString()
				duplicate.AccountIdentity = "second@example.com"
				second, err := database.ConfigureSDKTarget(ctx, user.ID, cap.TargetConfiguration{TargetID: uuid.NewString(), ProviderID: target.ProviderID, ProviderVersion: 1, SpaceID: space.ID, Label: "Second account", Capabilities: []string{"tasks.create"}, CallerApps: []string{}, Browser: &duplicate})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := database.ResolveSDKTargets(ctx, user.ID, cap.TargetResolve{Capability: "tasks.create", SpaceID: space.ID, ProviderID: target.ProviderID}); !errors.Is(err, db.ErrSDKTargetClarification) {
					t.Fatalf("ambiguous accounts guessed: %v", err)
				}
				exact, err := database.ResolveSDKTargets(ctx, user.ID, cap.TargetResolve{Capability: "tasks.create", TargetID: target.ID})
				if err != nil || len(exact) != 1 || exact[0].ID != target.ID {
					t.Fatalf("explicit account lost: %#v %v", exact, err)
				}
				if err := database.RevokeSDKTarget(ctx, user.ID, second.ID); err != nil {
					t.Fatal(err)
				}
			}
			draft := browseractions.Draft{Reference: origin + "/#draft/1", Thread: origin + "/#thread/1", Subject: "Review", Text: "I can meet Tuesday.", Recipients: []browseractions.Recipient{{Address: "boss@example.com"}}}
			task := browseractions.Task{Destination: browseractions.Destination{TargetID: target.ID, ContainerReference: origin + "/app/project/1", Label: "Work"}, Title: "Follow up", Text: "Discuss the email", Source: browseractions.Source{Reference: "https://mail.google.com/#thread/1", Label: "Source email"}}
			if name == "planner" {
				task.Destination.ContainerReference = cap.PlannerContainer(space.ID)
				task.Destination.Label = target.Label
			}
			input, _ := json.Marshal(map[string]any{"draftReference": draft.Reference, "expectedContentHash": browseractions.ContentHash(binding.AccountIdentity, draft), "recipients": draft.Recipients})
			if capability == "tasks.create" {
				input, _ = json.Marshal(task)
			}
			if mode == "read" {
				input, _ = json.Marshal(map[string]any{"threadReference": draft.Thread, "limit": 20})
			}
			if mode == "draft" {
				input, _ = json.Marshal(map[string]any{"recipients": draft.Recipients, "text": draft.Text, "replyTo": draft.Thread, "attachments": []any{}})
			}
			request := cap.Invocation{RequestID: uuid.NewString(), Capability: capability, CapabilityVersion: 1, ProviderID: target.ProviderID, ProviderVersion: 1, TargetID: target.ID, TargetRevision: target.Revision, Input: input, Deadline: time.Now().UTC().Add(time.Hour)}
			callID := uuid.NewString()
			var admitted *db.SDKInvocationRecord
			payload, _ := json.Marshal(map[string]any{"mode": "quick", "surface_id": "settings", "trigger": "message", "prompt": "Complete the reviewed pilot action", "context": []any{}, "timezone": "UTC", "idempotency_key": uuid.NewString()})
			global, _, createErr := database.CreateAIInvocationRecord(ctx, db.AIInvocationRecord{ID: "invocation_" + uuid.NewString(), UserID: user.ID, SurfaceID: "settings", Mode: "quick", Trigger: "message", State: "queued", IdempotencyKey: uuid.NewString(), RequestPayload: payload, ExpiresAt: request.Deadline})
			err = createErr
			if err == nil {
				_, effect := cap.AgentSDKIdentities(user.ID, global.ID, callID)
				admitted = &db.SDKInvocationRecord{InvocationID: global.ID, EffectID: effect, AdapterVersion: "sdk-browser:account-fixture"}
			}
			if err != nil {
				t.Fatal(err)
			}
			if name != "planner" && !strings.HasPrefix(admitted.AdapterVersion, "sdk-browser:") {
				t.Fatalf("wrong adapter: %s", admitted.AdapterVersion)
			}
			if device != nil {
				if _, err := database.AttachAIInvocationContext(ctx, user.ID, admitted.InvocationID, space.ID, device.ID, "browser_tab", binding.ScopeID, "Pilot", json.RawMessage(`["browser.inspect","browser.click","browser.type","browser.interact","browser.navigate"]`), json.RawMessage(`{}`)); err != nil {
					t.Fatal(err)
				}
			}
			runtimeID := "pilot-" + uuid.NewString()
			record, err := database.ActivateAIInvocationRuntime(ctx, admitted.InvocationID, "vercel-workflow", runtimeID)
			if err != nil {
				t.Fatal(err)
			}
			secret := []byte(strings.Repeat("s", 32))
			t.Setenv("MISTY_AGENT_RUNTIME_URL", "https://runtime.test")
			t.Setenv("MISTY_AGENT_RUNTIME_INTERNAL_API_URL", "https://api.test")
			t.Setenv("MISTY_AGENT_RUNTIME_CONTROL_SECRET", base64.StdEncoding.EncodeToString(secret))
			config, err := AgentRuntimeConfigFromEnv()
			if err != nil {
				t.Fatal(err)
			}
			service, err := NewSpacesService(database, nil, base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
			if err != nil {
				t.Fatal(err)
			}
			service.SetAgentRuntime(config)
			server := httptest.NewServer(service.MistyMCP())
			defer server.Close()
			token, err := TestingSignMCPAccessToken(secret, user.ID, record.ID, runtimeID, uuid.NewString(), "", time.Now(), time.Now().Add(5*time.Minute), true)
			if err != nil {
				t.Fatal(err)
			}
			client := mcp.NewClient(&mcp.Implementation{Name: "browser-pilot", Version: "1"}, nil)
			mcpSession, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: &http.Client{Transport: bearerRoundTripper{token: token, base: http.DefaultTransport}}, DisableStandaloneSSE: true}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer mcpSession.Close()
			catalog, err := mcpSession.ListTools(ctx, nil)
			if err != nil || len(catalog.Tools) < 1 {
				t.Fatalf("catalog %#v %v", catalog, err)
			}
			toolName := ""
			bindings, err := database.AgentSDKCapabilityBindings(ctx, user.ID, admitted.InvocationID)
			if err != nil {
				t.Fatal(err)
			}
			for _, pin := range bindings {
				if pin.TargetID != target.ID || pin.Capability != capability {
					continue
				}
				name := agenttools.ProviderToolName(agenttools.ProviderBinding{TargetID: pin.TargetID, TargetRevision: pin.TargetRevision, ProviderID: pin.ProviderID, ProviderVersion: pin.ProviderVersion, Capability: pin.Capability, CapabilityVersion: pin.CapabilityVersion, AdapterVersion: pin.AdapterVersion})
				for _, tool := range catalog.Tools {
					if tool.Name == name {
						toolName = name
					}
				}
			}
			if toolName == "" {
				t.Fatal("missing admitted semantic tool")
			}
			var writes atomic.Int32
			var changed atomic.Bool
			if device != nil {
				workerCtx, stop := context.WithCancel(ctx)
				defer stop()
				done := make(chan error, 1)
				go func() {
					page := browseractions.Page{URL: origin + "/#thread/1", Interactive: []browseractions.Element{{Ref: "commit", Role: "button", Name: "Send"}}}
					page.Target.ScopeID = binding.ScopeID
					page.Target.ProfileID = binding.ProfileID
					page.Target.Origin = origin
					page.Target.Trust = "host-observation"
					if mode == "login" {
						page.Target.Authentication = "required"
					}
					page.Semantic = browseractions.Observation{Adapter: name, Version: 1, Account: binding.AccountIdentity, Thread: draft.Thread, Draft: &draft}
					page.Semantic.Message = origin + "/#message/identified"
					page.Semantic.Subject = draft.Subject
					page.Semantic.Text = "Original email body"
					page.Semantic.Recipients = draft.Recipients
					if mode == "draft" {
						copy := draft
						copy.Text = "Old unsent body"
						page.Semantic.Draft = &copy
						page.Interactive = append(page.Interactive, browseractions.Element{Ref: "body", Role: "textbox", Name: "Message Body"})
					}
					if name == "todoist" {
						prepared := task
						prepared.Text = task.Text + "\n\n" + task.Source.Label + ": " + task.Source.Reference
						page.Semantic.Draft = nil
						page.Semantic.Task = &prepared
						page.Interactive[0].Name = "Add task"
					}
					for workerCtx.Err() == nil {
						job, lease, err := database.ClaimWorkflowDeviceNodeJob(user.ID, device.ID, 30*time.Second, 2)
						if errors.Is(err, db.ErrAgentJobNotFound) {
							time.Sleep(10 * time.Millisecond)
							continue
						}
						if err != nil {
							done <- err
							return
						}
						if job == nil {
							time.Sleep(10 * time.Millisecond)
							continue
						}
						if job.RunID != record.ID || job.ScopeID != binding.ScopeID {
							done <- cap.ErrInvalid
							return
						}
						if _, err := database.BeginWorkflowDeviceNodeJob(user.ID, device.ID, job.ID, lease); err != nil {
							done <- err
							return
						}
						output := json.RawMessage(`{"ok":true}`)
						switch job.Operation {
						case "browser.inspect":
							if changed.Load() {
								switch mode {
								case "account":
									page.Semantic.Account = "other@example.com"
								case "recipients":
									if page.Semantic.Draft != nil {
										page.Semantic.Draft.Recipients = []browseractions.Recipient{{Address: "other@example.com"}}
									}
								case "content":
									if page.Semantic.Draft != nil {
										page.Semantic.Draft.Text = "Unreviewed body"
									}
								case "destination":
									if page.Semantic.Task != nil {
										page.Semantic.Task.Destination.ContainerReference = origin + "/app/project/other"
									}
								}
							}
							page.DocumentID = uuid.NewString()
							output, _ = json.Marshal(page)
						case "browser.interact":
							var input struct{ Action struct{ Kind, Text string } }
							if json.Unmarshal(job.Input, &input) != nil || input.Action.Kind != "fill" || page.Semantic.Draft == nil {
								done <- cap.ErrInvalid
								return
							}
							page.Semantic.Draft.Text = input.Action.Text
						case "browser.click":
							if mode == "stale" && changed.Swap(false) {
								if _, err := database.FinishWorkflowDeviceNodeJob(user.ID, device.ID, job.ID, lease, "failed", nil, "browser_snapshot_stale"); err != nil {
									done <- err
									return
								}
								continue
							}
							writes.Add(1)
							if mode != "unconfirmed" && page.Semantic.Draft != nil {
								sent := *page.Semantic.Draft
								sent.Reference = origin + "/#sent/new"
								page.Semantic.Draft = nil
								page.Semantic.Sent = &sent
							}
							if page.Semantic.Task != nil {
								page.Semantic.Task.Reference = origin + "/app/task/new"
							}
						default:
							done <- cap.ErrInvalid
							return
						}
						state, code := "completed", ""
						if job.Operation == "browser.click" && (mode == "lost" || mode == "unconfirmed") {
							state, code = "uncertain", "device_execution_uncertain"
							output = nil
						}
						if _, err := database.FinishWorkflowDeviceNodeJob(user.ID, device.ID, job.ID, lease, state, output, code); err != nil {
							done <- err
							return
						}
					}
					done <- nil
				}()
				defer func() {
					stop()
					select {
					case err := <-done:
						if err != nil {
							t.Error(err)
						}
					case <-time.After(time.Second):
						t.Error("device worker did not stop")
					}
				}()
			}
			var args map[string]any
			_ = json.Unmarshal(input, &args)
			params := &mcp.CallToolParams{Name: toolName, Arguments: args, Meta: mcp.Meta{"misty/call_id": callID, "misty/approval_hook_token": "pilot-review", "misty/device_hook_token": "pilot-device-wait"}}
			pending, err := mcpSession.CallTool(ctx, params)
			if mode == "login" {
				raw, _ := json.Marshal(pending)
				if err != nil || pending.IsError || pending.Meta["misty/intervention_wait"] == nil || writes.Load() != 0 {
					t.Fatalf("missing sign-in intervention: %s %v", raw, err)
				}
				replay, err := mcpSession.CallTool(ctx, params)
				if err != nil || replay.IsError || replay.Meta["misty/intervention_wait"] == nil {
					t.Fatalf("lost pause response not recoverable: %#v %v", replay, err)
				}
				return
			}

			if mode == "read" || mode == "draft" {
				raw, _ := json.Marshal(pending)
				if err != nil || pending.IsError || pending.Meta["misty/approval"] != nil {
					t.Fatalf("autonomous preparation failed: %s %v", raw, err)
				}
				expected := "Original email body"
				if mode == "draft" {
					expected = "contentHash"
				}
				if !strings.Contains(string(raw), expected) || writes.Load() != 0 {
					t.Fatalf("preparation result unverified or sent: %s", raw)
				}
				return
			}

			if err != nil || pending.IsError || pending.Meta["misty/approval"] == nil {
				t.Fatalf("review required: %#v %v", pending, err)
			}
			if writes.Load() != 0 {
				t.Fatal("committed before review")
			}
			approvals, err := database.AgentPendingBrowserApprovals(ctx, user.ID, "", 20)
			if err != nil || len(approvals.Approvals) != 1 {
				t.Fatalf("approval inventory: %#v %v", approvals, err)
			}
			router := chi.NewRouter()
			router.Get("/me/agent-approvals/{approvalID}", service.AgentBrowserApprovalReview())
			router.Post("/me/agent-invocations/{runID}/approvals/{approvalID}", service.AgentBrowserApprovalDecision())
			bearer := newConversationTestBearerToken(t, database, user.ID)
			review := performConversationRequest(t, router, http.MethodGet, "/me/agent-approvals/"+approvals.Approvals[0].ID, bearer, nil)
			expected := "Discuss the email"
			if capability == "inbox.send" {
				expected = draft.Text
			}
			if review.Code != 200 || !strings.Contains(review.Body.String(), expected) {
				t.Fatalf("missing exact review body: %d %s", review.Code, review.Body.String())
			}
			decision := performConversationRequest(t, router, http.MethodPost, "/me/agent-invocations/"+db.SDKPublicRunID(record.ID)+"/approvals/"+approvals.Approvals[0].ID, bearer, map[string]bool{"approved": true})
			if decision.Code != 204 {
				t.Fatalf("approval: %s", decision.Body.String())
			}
			changed.Store(true)
			if mode == "cancel" {
				if err := database.CancelMistyInvocationChildren(ctx, user.ID, admitted.InvocationID); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "disconnected" {
				if err := database.RevokeTrustedDevice(user.ID, device.ID); err != nil {
					t.Fatal(err)
				}
			}
			result, err := mcpSession.CallTool(ctx, params)
			invalidated := mode == "account" || mode == "recipients" || mode == "content" || mode == "destination" || mode == "cancel" || mode == "disconnected"
			if invalidated {
				if err == nil && !result.IsError {
					t.Fatal("changed/cancelled action executed")
				}
				if writes.Load() != 0 {
					t.Fatal("write after invalidation")
				}
				if mode == "account" || mode == "recipients" || mode == "content" || mode == "destination" {
					approval, _, err := database.SDKApprovalReview(ctx, user.ID, approvals.Approvals[0].ID)
					if err != nil || approval.State != "expired" {
						t.Fatalf("review not retired: %#v %v", approval, err)
					}
				}
				return
			}
			if mode == "unconfirmed" {
				raw, _ := json.Marshal(result)
				if err != nil || !strings.Contains(string(raw), "uncertain") {
					t.Fatalf("uncertainty concealed: %s %v", raw, err)
				}
				_, _ = mcpSession.CallTool(ctx, params)
				if writes.Load() != 1 {
					t.Fatal("uncertain write repeated")
				}
				return
			}
			if err != nil || result.IsError {
				raw, _ := json.Marshal(result)
				t.Fatalf("approved execution: %s %v", raw, err)
			}
			replay, err := mcpSession.CallTool(ctx, params)
			if err != nil || replay.IsError {
				t.Fatalf("replay: %#v %v", replay, err)
			}
			if device != nil && writes.Load() != 1 {
				t.Fatalf("duplicate effect: %d", writes.Load())
			}
			if name == "planner" {
				tasks, err := database.SpaceTasks(ctx, user.ID, space.ID, db.SpaceTaskQuery{})
				if err != nil || len(tasks) != 1 || tasks[0].Notes != task.Text {
					t.Fatalf("Planner verification: %#v %v", tasks, err)
				}
			}
		})
	}
}

package api

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/aimodels"
	"github.com/kannachi323/misty/server/internal/modelruntime"
	"github.com/kannachi323/misty/server/internal/platform/security"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// The desktop's screen loop calls the pass-through once per action while its
// browser.act job runs. Each call is metered to the run without spending the
// run's agent turns, and nothing is forwarded once the job ends.
func TestScreenModelPassThroughForALiveActJob(t *testing.T) {
	database := openPresenceTestDatabase(t)
	ctx := t.Context()
	user, err := database.CreateUser("Screen model", uniqueTestEmail("screen-model"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	var forwarded []map[string]any
	runtime := modelruntime.TestingNewFake(t, func(call modelruntime.TestingCall) (int, any) {
		forwarded = append(forwarded, call.Body)
		return http.StatusOK, map[string]any{"text": "<action>click</action>", "usage": map[string]int{"inputTokens": 900, "outputTokens": 40}}
	})
	service, err := NewSpacesService(database, nil, base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	if err != nil {
		t.Fatal(err)
	}
	service.SetModels(runtime.Client)
	space, err := database.TestingCreateSpace(ctx, user.ID, "Screen")
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := database.CreateAIInvocationRecord(ctx, db.AIInvocationRecord{ID: "invocation_" + uuid.NewString(), UserID: user.ID, SpaceID: space.ID, SurfaceID: "global", Mode: "drawer", Trigger: "message", State: "queued", IdempotencyKey: uuid.NewString(), RequestPayload: json.RawMessage(`{"model_id":"openai/gpt-6-luna"}`), ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	key, _, _ := ed25519.GenerateKey(rand.Reader)
	device, err := database.RegisterTrustedDevice(user.ID, "Screen Mac", base64.RawURLEncoding.EncodeToString(key), "macos", "", json.RawMessage(`[]`), json.RawMessage(`{"browser_tools":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.AttachAIInvocationContext(ctx, user.ID, run.ID, space.ID, device.ID, "browser_tab", "act-scope", "act-scope", json.RawMessage(`["browser.visual","browser.act"]`), json.RawMessage(`{"app_id":"browser","window_label":"main"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ActivateAIInvocationRuntime(ctx, run.ID, "vercel-workflow", "screen-runtime"); err != nil {
		t.Fatal(err)
	}
	// Prepare freezes the run's routes; screen calls use its vision route.
	if _, err := database.FreezeAIModelRoutes(ctx, user.ID, run.ID, []aimodels.Route{
		{Role: "agent", Model: "openai/gpt-6-luna", Reasoning: "low", Enabled: true},
		{Role: "vision", Model: "openai/gpt-6-luna", Reasoning: "low", Enabled: true},
	}, nil); err != nil {
		t.Fatal(err)
	}
	job, err := database.QueueAIInvocationDeviceNodeJob(ctx, user.ID, run.ID, "act-node", 1, "act-scope", "browser.act", "browser.act", json.RawMessage(`{"goal":"Draw a house"}`), json.RawMessage(`{}`), json.RawMessage(`{"type":"object"}`), json.RawMessage(`{"type":"object"}`))
	if err != nil {
		t.Fatal(err)
	}

	sid, refresh := "sid-"+uniqueTestEmail("screen"), "refresh-"+uniqueTestEmail("screen")
	if err := database.CreateRefreshSession(ctx, security.HashToken(sid), security.HashToken(refresh), user.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	signer, err := security.SessionSignerFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	access, err := signer.Mint(user.ID, sid, "access", time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Post("/me/screen-model/{jobID}", service.ScreenModel())
	call := func(n string) *httptest.ResponseRecorder {
		t.Helper()
		body := []byte(`{"messages":[{"role":"user","content":"plan"}]}`)
		r := httptest.NewRequest(http.MethodPost, "/me/screen-model/"+job.ID+"?call="+n, bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: TestingSessionCookieName, Value: access})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}

	if response := call("0"); response.Code != http.StatusForbidden {
		t.Fatalf("forwarded before the device claimed the job: %d %s", response.Code, response.Body.String())
	}
	claimed, token, err := database.ClaimWorkflowDeviceNodeJob(user.ID, device.ID, time.Minute, 2)
	if err != nil || claimed == nil || claimed.ID != job.ID {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	if _, err := database.BeginWorkflowDeviceNodeJob(user.ID, device.ID, job.ID, token); err != nil {
		t.Fatal(err)
	}
	// More calls than the run's ordinary agent-turn allowance.
	for n := range 25 {
		response := call(strconv.Itoa(n))
		var completion struct {
			Choices []struct {
				Message struct{ Content string } `json:"message"`
			} `json:"choices"`
			Usage struct {
				PromptTokens int64 `json:"prompt_tokens"`
			} `json:"usage"`
		}
		_ = json.Unmarshal(response.Body.Bytes(), &completion)
		if response.Code != http.StatusOK || len(completion.Choices) != 1 || completion.Choices[0].Message.Content != "<action>click</action>" || completion.Usage.PromptTokens != 900 {
			t.Fatalf("call %d: %d %s", n, response.Code, response.Body.String())
		}
	}
	if len(forwarded) != 25 || forwarded[0]["model"] != "openai/gpt-6-luna" || forwarded[0]["maxOutputTokens"] != float64(6000) || forwarded[0]["route"].(map[string]any)["provider"] != "instance" {
		t.Fatalf("forwarded %d calls, first %v", len(forwarded), forwarded[0])
	}
	if response := call("80"); response.Code != http.StatusBadRequest {
		t.Fatalf("call beyond the job's limit: %d", response.Code)
	}
	if _, err := database.FinishWorkflowDeviceNodeJob(user.ID, device.ID, job.ID, token, "completed", json.RawMessage(`{"status":"done"}`), ""); err != nil {
		t.Fatal(err)
	}
	if response := call("25"); response.Code != http.StatusForbidden || len(forwarded) != 25 {
		t.Fatalf("forwarded after the job ended: %d", response.Code)
	}
}

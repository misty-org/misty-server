package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestAgentRuntimeStartReceiptRequiresSignedPinnedAdmission(t *testing.T) {
	database := openPresenceTestDatabase(t)
	owner, err := database.CreateUser("Start receipt", uniqueTestEmail("start-receipt"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	id := "invocation_" + uuid.NewString()
	if _, _, err := database.CreateAIInvocationRecord(t.Context(), db.AIInvocationRecord{ID: id, UserID: owner.ID, Mode: "quick", SurfaceID: "settings", Trigger: "message", State: "queued", IdempotencyKey: uuid.NewString(), RequestPayload: json.RawMessage(`{"prompt":"hello"}`), ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.BindAgentRuntime(t.Context(), id, "https://worker.test", "https://api.test"); err != nil {
		t.Fatal(err)
	}
	secret := []byte(strings.Repeat("s", 32))
	t.Setenv("MISTY_AGENT_RUNTIME_URL", "https://worker.test")
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
	router := chi.NewRouter()
	router.Post("/internal/agent-runtime/runs/{runID}/start-receipt", service.AgentRuntimeStartReceipt())
	invoke := func(signed bool, callback, token, runtime string) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"adapter_version": db.BetaAgentRuntimeAdapter, "callback_url": callback, "claim_token": token, "runtime_run_id": runtime})
		path := "/internal/agent-runtime/runs/" + id + "/start-receipt"
		request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		request.Header.Set("Idempotency-Key", id+":start")
		if signed {
			timestamp := strconv.FormatInt(time.Now().Unix(), 10)
			request.Header.Set("X-Misty-Agent-Timestamp", timestamp)
			request.Header.Set("X-Misty-Agent-Signature", TestingAgentRuntimeSignature(secret, http.MethodPost, path, timestamp, body))
		}
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		return recorder
	}
	if r := invoke(false, "https://api.test", "", ""); r.Code != 401 {
		t.Fatalf("unsigned claim: %d %s", r.Code, r.Body)
	}
	if r := invoke(true, "https://replacement.test", "", ""); r.Code == 200 {
		t.Fatal("wrong pinned callback allowed")
	}
	r := invoke(true, "https://api.test", "", "")
	var receipt db.AgentRuntimeStartReceipt
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &receipt) != nil || !receipt.Claimed {
		t.Fatalf("claim: %d %s", r.Code, r.Body)
	}
	if r := invoke(true, "https://api.test", receipt.ClaimToken, "engine-once"); r.Code != 200 {
		t.Fatalf("record: %d %s", r.Code, r.Body)
	}
	r = invoke(true, "https://api.test", "", "")
	var replay db.AgentRuntimeStartReceipt
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &replay) != nil || replay.Claimed || replay.RuntimeRunID != "engine-once" {
		t.Fatalf("replay: %d %s", r.Code, r.Body)
	}
}

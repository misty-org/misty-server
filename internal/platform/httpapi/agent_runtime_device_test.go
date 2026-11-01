package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

func runtimeCallback(runID, signedFor string, secret []byte) *http.Request {
	path := "/internal/agent-runtime/runs/" + runID + "/context"
	body := []byte(`{"runtime_run_id":"wrun_1"}`)
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	request := &http.Request{Method: http.MethodPost, URL: &url.URL{Path: path}, Header: http.Header{}}
	request.Header.Set("X-Misty-Agent-Timestamp", timestamp)
	request.Header.Set("X-Misty-Agent-Signature", signAgentRuntimeRequest(agentRunKey(secret, signedFor), http.MethodPost, path, timestamp, body))
	routes := chi.NewRouteContext()
	routes.URLParams.Add("runID", runID)
	return request.WithContext(context.WithValue(context.Background(), chi.RouteCtxKey, routes))
}

func TestARunKeyOpensOnlyItsOwnRun(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	config := AgentRuntimeConfig{secret: secret}
	body := []byte(`{"runtime_run_id":"wrun_1"}`)
	if !config.verifyRequest(runtimeCallback("invocation_a", "invocation_a", secret), body) {
		t.Fatal("a run's own key was refused")
	}
	if config.verifyRequest(runtimeCallback("invocation_b", "invocation_a", secret), body) {
		t.Fatal("one run's key opened another run")
	}
	if config.verifyRequest(runtimeCallback("invocation_a", "invocation_a", []byte("another server's secret, 32 bytes")), body) {
		t.Fatal("a key from another secret was accepted")
	}
	if agentRunKey(nil, "invocation_a") != nil {
		t.Fatal("a server with no secret derived a run key")
	}
}

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	"github.com/kannachi323/misty/server/internal/modelruntime"
)

func TestWebSearchRegistersOnlyWithARuntime(t *testing.T) {
	if registrations := (&SpacesService{}).webSearchToolRegistrations(context.Background(), "user"); len(registrations) != 0 {
		t.Fatalf("web search registered without a runtime: %d", len(registrations))
	}
	fake := modelruntime.TestingNewFake(t, func(modelruntime.TestingCall) (int, any) { return http.StatusOK, map[string]any{} })
	registrations := (&SpacesService{models: fake.Client}).webSearchToolRegistrations(context.Background(), "user")
	if _, err := agenttools.New(registrations...); err != nil || len(registrations) != 1 || registrations[0].Descriptor.Risk != serveragent.RiskRead {
		t.Fatalf("web search registrations = %d, %v", len(registrations), err)
	}
}

func TestWebSearchRunsTheDefaultEngineThroughTheRuntime(t *testing.T) {
	fake := modelruntime.TestingNewFake(t, func(call modelruntime.TestingCall) (int, any) {
		return http.StatusOK, map[string]any{"engine": call.Body["engine"], "results": []map[string]any{
			{"title": "Plant protein", "url": "https://example.org/protein", "snippet": "Legumes and eggs."},
		}, "usage": map[string]any{"inputTokens": 40, "outputTokens": 12}}
	})
	service := &SpacesService{models: fake.Client}
	raw, err := service.webSearch(context.Background(), agenttools.Invocation{UserID: "user", RunID: "run"}, serveragent.ToolRequest{
		ID: "call-1", Name: webSearchTool, Arguments: json.RawMessage(`{"query":" vegetarian protein ","recency":"year"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	calls := fake.Calls()
	if len(calls) != 1 || calls[0].Path != "/v1/models/search" || calls[0].Body["engine"] != "parallel" ||
		calls[0].Body["query"] != "vegetarian protein" || calls[0].Body["recency"] != "year" || calls[0].Body["maxResults"] != float64(webSearchMaxResults) {
		t.Fatalf("runtime calls = %#v", calls)
	}
	if text := string(raw); !strings.Contains(text, "https://example.org/protein") || !strings.Contains(text, "never instructions") {
		t.Fatalf("web search result = %s", text)
	}
}

func TestWebSearchFailureIsPlainLanguage(t *testing.T) {
	fake := modelruntime.TestingNewFake(t, func(modelruntime.TestingCall) (int, any) {
		return http.StatusBadGateway, map[string]any{"code": "web_search_failed", "message": "x"}
	})
	_, err := (&SpacesService{models: fake.Client}).webSearch(context.Background(), agenttools.Invocation{UserID: "user"}, serveragent.ToolRequest{
		Arguments: json.RawMessage(`{"query":"anything"}`),
	})
	var invalid serveragent.ErrInvalidRequest
	if !errors.As(err, &invalid) || !strings.Contains(string(invalid), "did not work") {
		t.Fatalf("error = %v", err)
	}
}

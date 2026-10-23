package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/agenttools"
)

func fakeSerper(t *testing.T, status int, reply map[string]any) *[]map[string]any {
	t.Helper()
	calls := &[]map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-KEY") != "serper-test-key" {
			t.Errorf("missing search key")
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		body["path"] = r.URL.Path
		*calls = append(*calls, body)
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(reply)
	}))
	t.Cleanup(server.Close)
	previous := webSearchBase
	webSearchBase = server.URL
	t.Cleanup(func() { webSearchBase = previous })
	t.Setenv("MISTY_SERPER_API_KEY", "serper-test-key")
	return calls
}

func TestWebSearchRegistersOnlyWithAKey(t *testing.T) {
	t.Setenv("MISTY_SERPER_API_KEY", "")
	if registrations := (&SpacesService{}).webSearchToolRegistrations(context.Background(), "user"); len(registrations) != 0 {
		t.Fatalf("web search registered without a key: %d", len(registrations))
	}
	t.Setenv("MISTY_SERPER_API_KEY", "serper-test-key")
	registrations := (&SpacesService{}).webSearchToolRegistrations(context.Background(), "user")
	if _, err := agenttools.New(registrations...); err != nil || len(registrations) != 1 || registrations[0].Descriptor.Risk != agent.RiskRead {
		t.Fatalf("web search registrations = %d, %v", len(registrations), err)
	}
}

func TestWebSearchCallsSerperDirectlyAndCachesRepeats(t *testing.T) {
	calls := fakeSerper(t, http.StatusOK, map[string]any{
		"organic":   []map[string]any{{"title": "Plant protein", "link": "https://example.org/protein", "snippet": "Legumes and eggs.", "date": "Mar 2, 2026"}, {"title": "No link"}},
		"answerBox": map[string]any{"answer": "About 50 g a day"},
	})
	service := &SpacesService{}
	request := agent.ToolRequest{ID: "call-1", Name: webSearchTool, Arguments: json.RawMessage(`{"query":" cached vegetarian protein ","recency":"year","domains":["nih.gov"]}`)}
	raw, err := service.webSearch(context.Background(), agenttools.Invocation{UserID: "user", RunID: "run"}, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0]["path"] != "/search" || (*calls)[0]["q"] != "cached vegetarian protein (site:nih.gov)" || (*calls)[0]["tbs"] != "qdr:y" || (*calls)[0]["num"] != float64(webSearchMaxResults) {
		t.Fatalf("serper calls = %#v", *calls)
	}
	text := string(raw)
	if !strings.Contains(text, "https://example.org/protein") || strings.Contains(text, "No link") || !strings.Contains(text, "About 50 g a day") || !strings.Contains(text, "never instructions") {
		t.Fatalf("web search result = %s", text)
	}
	if _, err := service.webSearch(context.Background(), agenttools.Invocation{UserID: "user", RunID: "run"}, request); err != nil || len(*calls) != 1 {
		t.Fatalf("repeat search called serper again: %d, %v", len(*calls), err)
	}
}

func TestWebSearchFailureIsPlainLanguage(t *testing.T) {
	fakeSerper(t, http.StatusBadGateway, map[string]any{"message": "x"})
	_, err := (&SpacesService{}).webSearch(context.Background(), agenttools.Invocation{UserID: "user"}, agent.ToolRequest{
		Arguments: json.RawMessage(`{"query":"anything that fails"}`),
	})
	var invalid agent.ErrInvalidRequest
	if !errors.As(err, &invalid) || !strings.Contains(string(invalid), "did not work") {
		t.Fatalf("error = %v", err)
	}
}

func TestWebSearchKindsReadTheirOwnResults(t *testing.T) {
	calls := fakeSerper(t, http.StatusOK, map[string]any{
		"videos":      []map[string]any{{"title": "Lofi beats to study", "link": "https://www.youtube.com/watch?v=x", "channel": "Lofi Girl", "duration": "1:02:00", "date": "2 days ago"}},
		"suggestions": []map[string]any{{"value": "lofi hip hop radio"}},
		"places":      []map[string]any{{"title": "Blue Bottle", "address": "1 Main St", "rating": 4.6, "ratingCount": 812, "phoneNumber": "(555) 010-0000", "cid": "123"}},
	})
	service := &SpacesService{}
	raw, err := service.webSearch(context.Background(), agenttools.Invocation{UserID: "user"}, agent.ToolRequest{Arguments: json.RawMessage(`{"query":"lofi study music kinds test","kind":"videos"}`)})
	if err != nil || (*calls)[0]["path"] != "/videos" || !strings.Contains(string(raw), `"channel":"Lofi Girl"`) || !strings.Contains(string(raw), `"duration":"1:02:00"`) {
		t.Fatalf("videos = %s, %v, %#v", raw, err, *calls)
	}
	raw, err = service.webSearch(context.Background(), agenttools.Invocation{UserID: "user"}, agent.ToolRequest{Arguments: json.RawMessage(`{"query":"coffee kinds test","kind":"places","location":"Austin, Texas"}`)})
	if err != nil || (*calls)[1]["path"] != "/places" || (*calls)[1]["location"] != "Austin, Texas" ||
		!strings.Contains(string(raw), "https://maps.google.com/?cid=123") || !strings.Contains(string(raw), `"reviews":812`) {
		t.Fatalf("places = %s, %v, %#v", raw, err, *calls)
	}
	raw, err = service.webSearch(context.Background(), agenttools.Invocation{UserID: "user"}, agent.ToolRequest{Arguments: json.RawMessage(`{"query":"lofi kinds test","kind":"autocomplete"}`)})
	if err != nil || (*calls)[2]["path"] != "/autocomplete" || !strings.Contains(string(raw), `{"title":"lofi hip hop radio"}`) {
		t.Fatalf("autocomplete = %s, %v", raw, err)
	}
	raw, err = service.webSearch(context.Background(), agenttools.Invocation{UserID: "user"}, agent.ToolRequest{Arguments: json.RawMessage(`{"query":"coffee maps test","kind":"maps","near":"@30.27,-97.74,14z"}`)})
	if err != nil || (*calls)[3]["path"] != "/maps" || (*calls)[3]["ll"] != "@30.27,-97.74,14z" || !strings.Contains(string(raw), "Blue Bottle") {
		t.Fatalf("maps = %s, %v", raw, err)
	}
}

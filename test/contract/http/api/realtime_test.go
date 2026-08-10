package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

func TestRealtimeConnectRejectsNonUpgradeBeforeTicketLookup(t *testing.T) {
	service := NewRealtimeService(nil, "")
	request := httptest.NewRequest(http.MethodGet, "/api/realtime?ticket=still-valid", nil)
	recorder := httptest.NewRecorder()

	service.Connect().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUpgradeRequired {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUpgradeRequired)
	}
	if recorder.Header().Get("Upgrade") != "websocket" {
		t.Fatalf("Upgrade header = %q, want websocket", recorder.Header().Get("Upgrade"))
	}
	var body map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["code"] != "websocket_upgrade_required" {
		t.Fatalf("code = %q, want websocket_upgrade_required", body["code"])
	}
}

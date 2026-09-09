package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func testSDKApprovalReview(t *testing.T, database *db.Database, user, approvalID, targetID, spaceID string, input json.RawMessage, deniedTokens ...string) {
	t.Helper()
	// A fresh service must restore exactly the persisted proposal after restart.
	service, err := NewSpacesService(database, nil, base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Get("/me/capability-approvals", service.SDKCapabilityApprovals())
	router.Get("/me/capability-approvals/{approvalID}", service.SDKCapabilityApprovalReview())
	path := "/me/capability-approvals/" + approvalID
	account := newConversationTestBearerToken(t, database, user)
	response := performConversationRequest(t, router, http.MethodGet, path, account, nil)
	var result struct {
		Approval struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"approval"`
		Review struct {
			Execution cap.Execution `json:"execution"`
			Target    cap.Target    `json:"target"`
			Effects   cap.Effects   `json:"effects"`
		} `json:"review"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Approval.ID != approvalID || result.Approval.State != "pending" || result.Review.Execution.Capability != "habits.record" || result.Review.Target.ID != targetID || result.Review.Target.SpaceID != spaceID || !cap.EqualJSON(result.Review.Execution.Input, input) || result.Review.Effects.Kind != "write" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("approval is not reviewable: %d %s", response.Code, response.Body.String())
	}
	for _, private := range []string{"private-provider-token", "habits.example.com/execute", "hook_token", "ciphertext"} {
		if strings.Contains(response.Body.String(), private) {
			t.Fatalf("review leaked %s", private)
		}
	}
	listed := performConversationRequest(t, router, http.MethodGet, "/me/capability-approvals?limit=20", account, nil)
	if listed.Code != 200 || !strings.Contains(listed.Body.String(), approvalID) || strings.Contains(listed.Body.String(), "ciphertext") || strings.Contains(listed.Body.String(), "execution") || listed.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("pending approval discovery: %d %s", listed.Code, listed.Body.String())
	}
	_, stored, err := database.SDKApprovalReview(t.Context(), user, approvalID)
	if err != nil || stored.EffectID != result.Review.Execution.EffectID || bytes.Contains(stored.Ciphertext, []byte("habits.record")) {
		t.Fatalf("review not protected: %v", err)
	}
	other, err := database.CreateUser("Other reviewer", uuid.NewString()+"@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	otherList := performConversationRequest(t, router, http.MethodGet, "/me/capability-approvals", newConversationTestBearerToken(t, database, other.ID), nil)
	if otherList.Code != 200 || strings.Contains(otherList.Body.String(), approvalID) {
		t.Fatalf("cross-user discovery: %d %s", otherList.Code, otherList.Body.String())
	}
	deniedTokens = append(deniedTokens, newConversationTestBearerToken(t, database, other.ID))
	for _, token := range deniedTokens {
		denied := performConversationRequest(t, router, http.MethodGet, path, token, nil)
		if denied.Code != 401 && denied.Code != 403 {
			t.Fatalf("unauthorized approval read: %d %s", denied.Code, denied.Body.String())
		}
	}
}

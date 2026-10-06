package api

import (
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"net/http"
	"testing"
	"time"
)

func TestAccountFolderProposalRestoresAndRequiresOwner(t *testing.T) {
	database := openPresenceTestDatabase(t)
	owner, err := database.CreateUser("Folder owner", uniqueTestEmail("folder-owner"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	other, err := database.CreateUser("Other owner", uniqueTestEmail("folder-other"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	invocationID, artifactID := "invocation_"+uuid.NewString(), "artifact_"+uuid.NewString()
	_, _, err = database.CreateAIInvocationRecord(t.Context(), db.AIInvocationRecord{ID: invocationID, UserID: owner.ID, SurfaceID: "files", Mode: "drawer", Trigger: "message", State: "queued", IdempotencyKey: uuid.NewString(), RequestPayload: json.RawMessage(`{}`), ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"id": artifactID, "invocationId": invocationID, "schemaVersion": 1, "kind": "file_plan", "title": "Organize notes", "summary": "Move one note", "sources": []any{}, "target": map[string]string{"kind": "files.scope", "id": "grant"}, "operations": map[string]any{"steps": []any{}}, "risk": "consequential", "approvalPolicy": "always_confirm", "idempotencyKey": uuid.NewString(), "state": "proposed", "expiresAt": time.Now().Add(time.Hour)})
	if err := database.UpsertAIArtifact(t.Context(), owner.ID, invocationID, payload); err != nil {
		t.Fatal(err)
	}
	_, recovered, err := database.AIConversationReview(t.Context(), owner.ID, invocationID)
	if err != nil || len(recovered) == 0 {
		t.Fatal("proposal not recovered", err)
	}
	_, foreign, err := database.AIConversationReview(t.Context(), other.ID, invocationID)
	if err != nil || len(foreign) != 0 {
		t.Fatal("proposal crossed accounts", err)
	}
	service := NewAIService(database, nil)
	router := chi.NewRouter()
	router.Post("/ai/artifacts/{artifactID}/decision", service.DecideArtifact())
	endpoint := "/ai/artifacts/" + artifactID + "/decision"
	res := performConversationRequest(t, router, "POST", endpoint, newConversationTestBearerToken(t, database, other.ID), map[string]string{"decision": "accept"})
	if res.Code != http.StatusNotFound {
		t.Fatalf("foreign approval status %d", res.Code)
	}
	token := newConversationTestBearerToken(t, database, owner.ID)
	res = performConversationRequest(t, router, "POST", endpoint, token, map[string]string{"decision": "accept"})
	if res.Code != http.StatusOK {
		t.Fatalf("account-scoped approval status %d: %s", res.Code, res.Body.String())
	}
	_, recovered, err = database.AIConversationReview(t.Context(), owner.ID, invocationID)
	if err != nil || len(recovered) != 0 {
		t.Fatal("decided proposal offered again", err)
	}
	res = performConversationRequest(t, router, "POST", endpoint, token, map[string]string{"decision": "accept"})
	if res.Code != http.StatusConflict {
		t.Fatalf("duplicate approval status %d", res.Code)
	}
}

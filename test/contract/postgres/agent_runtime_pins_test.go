package db

import (
	"database/sql"
	"encoding/json"
	"github.com/google/uuid"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
	"strings"
	"testing"
	"time"
)

func TestAgentRuntimeDeploymentPinSurvivesConfigurationChanges(t *testing.T) {
	database := openTestDatabase(t)
	user, err := database.CreateUser("Pinned runtime", uuid.NewString()+"@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	id := "invocation_" + uuid.NewString()
	_, _, err = database.CreateAIInvocationRecord(t.Context(), AIInvocationRecord{ID: id, UserID: user.ID, Mode: "quick", SurfaceID: "settings", Trigger: "message", State: "queued", IdempotencyKey: uuid.NewString(), RequestPayload: json.RawMessage(`{"prompt":"hello"}`), ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	first, err := database.BindAgentRuntime(t.Context(), id, "https://worker-v1.example.org", "https://api-v1.example.org")
	if err != nil {
		t.Fatal(err)
	}
	if first.AdapterVersion != BetaAgentRuntimeAdapter {
		t.Fatalf("unversioned admission: %#v", first)
	}
	again, err := database.BindAgentRuntime(t.Context(), id, "https://worker-v2.example.org", "https://api-v2.example.org")
	if err != nil || *again != *first {
		t.Fatalf("worker silently replaced: %#v %v", again, err)
	}
	for _, column := range []string{"runtime_adapter_version", "runtime_endpoint", "runtime_callback_endpoint"} {
		err := database.TestingSpaceTx(t.Context(), func(tx *sql.Tx) error {
			_, err := tx.ExecContext(t.Context(), `UPDATE ai_invocations SET `+column+`='replacement' WHERE id=$1`, id)
			return err
		})
		if err == nil || !strings.Contains(err.Error(), "agent_runtime_pin_immutable") {
			t.Fatalf("mutable %s: %v", column, err)
		}
	}
	if _, err := database.BindAgentRuntime(t.Context(), "invocation_"+uuid.NewString(), "https://worker.example.org", "https://api.example.org"); err == nil {
		t.Fatal("unknown run admitted")
	}
}

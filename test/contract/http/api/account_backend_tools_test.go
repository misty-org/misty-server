package api

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestAccountConnectedBackendExecutesWithoutAppInstallation(t *testing.T) {
	database := openPresenceTestDatabase(t)
	ctx := t.Context()
	user, err := database.CreateUser("Connected backend", uuid.NewString()+"@example.test", "password123")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MISTY_SDK_EXECUTION_ENABLED", "true")
	secret := []byte(strings.Repeat("s", 32))
	t.Setenv("MISTY_AGENT_RUNTIME_URL", "https://runtime.test")
	t.Setenv("MISTY_AGENT_RUNTIME_INTERNAL_API_URL", "https://api.test")
	t.Setenv("MISTY_AGENT_RUNTIME_CONTROL_SECRET", base64.StdEncoding.EncodeToString(secret))
	config, err := AgentRuntimeConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	key := []byte(strings.Repeat("k", 32))
	service, err := NewSpacesService(database, nil, base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatal(err)
	}
	service.SetAgentRuntime(config)
	connectionID := uuid.NewString()
	provider := cap.Provider{ID: "example.habits/backend", Version: 1, Label: "Habits", Route: cap.Route{Kind: "backend", ConnectionID: connectionID}}
	for _, name := range []string{"habits.list", "habits.record"} {
		effects := cap.Effects{Kind: "read", Approval: "none", Retry: "read_only", Incidental: []string{}}
		if name == "habits.record" {
			effects = cap.Effects{Kind: "write", Approval: "interactive", Retry: "idempotent", Incidental: []string{}}
		}
		provider.Capabilities = append(provider.Capabilities, cap.Definition{Name: name, Version: 1, Description: name, RequiredScopes: []string{name}, InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`), OutputSchema: json.RawMessage(`{"type":"array","items":{"type":"string"}}`), Effects: effects})
	}
	raw, _ := json.Marshal(provider)
	err = database.TestingWithRLSContext(ctx, db.TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO sdk_provider_versions(user_id,provider_id,version,app_id,definition) VALUES($1,$2,1,'example.habits',$3)`, user.ID, provider.ID, raw); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO sdk_provider_registrations(user_id,provider_id,version,app_id,app_version,installed_at,space_id,reported_state) VALUES($1,$2,1,'example.habits','connection-1',now(),'','available')`, user.ID, provider.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	encrypted := aead.Seal(nonce, nonce, []byte("private-provider-token"), []byte(fmt.Sprintf("misty-sdk-backend-v1:%s:example.habits:%s:1", user.ID, connectionID)))
	_, err = database.ConfigureSDKBackendConnection(ctx, user.ID, db.SDKBackendConnection{UserID: user.ID, AppID: "example.habits", ID: connectionID, EndpointURL: "https://habits.example.com/execute", BearerCiphertext: encrypted, KeyVersion: 1}, 0)
	if err != nil {
		t.Fatal(err)
	}
	target, err := database.ConfigureSDKTarget(ctx, user.ID, cap.TargetConfiguration{TargetID: uuid.NewString(), ProviderID: provider.ID, ProviderVersion: 1, Label: "My habits", Capabilities: []string{"habits.list", "habits.record"}, CallerApps: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the actual account Agent MCP path, reviews, duplicate writes,
	// interrupted responses and refusal to replan an uncertain external effect.
	testAIInvocationSDKAccount(t, database, service, user.ID, target.ID, secret)
}

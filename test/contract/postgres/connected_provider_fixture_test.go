package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func sdkInvocationFixture(t *testing.T) (*Database, context.Context, string, cap.Invocation) {
	t.Helper()
	database := openTestDatabase(t)
	ctx := t.Context()
	user, err := database.CreateUser("Invocation owner", "invocation-owner@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	document, _ := sdkInstallFixture(t, "example.habits")
	provider := document.Capabilities.Providers[0]
	seedConnectedProvider(t, database, user.ID, document.AppID, provider)
	if _, err := database.ConfigureSDKBackendConnection(ctx, user.ID, SDKBackendConnection{UserID: user.ID, AppID: document.AppID, ID: provider.Route.ConnectionID, EndpointURL: "https://habits.example.com/execute", BearerCiphertext: []byte(strings.Repeat("encrypted", 8)), KeyVersion: 1}, 0); err != nil {
		t.Fatal(err)
	}
	target, err := database.ConfigureSDKTarget(ctx, user.ID, cap.TargetConfiguration{TargetID: uuid.NewString(), ProviderID: provider.ID, ProviderVersion: 1, Label: "My habits", Capabilities: []string{"habits.list"}, CallerApps: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	return database, WithAppExecutionAuthority(ctx, AppRuntimeSession{UserID: user.ID, AppID: document.AppID, AuthorityGeneration: 1, Scopes: document.Scopes}), user.ID, cap.Invocation{RequestID: uuid.NewString(), Capability: "habits.list", CapabilityVersion: 1, ProviderID: provider.ID, ProviderVersion: 1, TargetID: target.ID, TargetRevision: target.Revision, Input: json.RawMessage(`{}`), Deadline: time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)}
}

// Imported provider definitions are account-owned. Fixtures deliberately create
// no app installations, grants or app bearer sessions.
func seedConnectedProvider(t *testing.T, database *Database, user, namespace string, provider cap.Provider) {
	t.Helper()
	raw, err := json.Marshal(provider)
	if err != nil {
		t.Fatal(err)
	}
	err = database.TestingWithRLSContext(t.Context(), TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO sdk_provider_versions(user_id,provider_id,version,app_id,definition) VALUES($1,$2,$3,$4,$5)`, user, provider.ID, provider.Version, namespace, raw); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO sdk_provider_registrations(user_id,provider_id,version,app_id,app_version,installed_at,space_id,reported_state) VALUES($1,$2,$3,$4,'connection-1',now(),'','available')`, user, provider.ID, provider.Version, namespace)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
func reportConnectedProvider(t *testing.T, database *Database, user, provider, state string) error {
	t.Helper()
	return database.TestingWithRLSContext(t.Context(), TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE sdk_provider_registrations SET reported_state=$3,observed_at=now() WHERE user_id=$1 AND provider_id=$2`, user, provider, state)
		return err
	})
}

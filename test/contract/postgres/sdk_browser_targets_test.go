package db

import (
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

func TestSDKBrowserTargetConfigurationPreservesProfilesAndRejectsBackendDispatch(t *testing.T) {
	database := openTestDatabase(t)
	ctx := t.Context()
	owner, err := database.CreateUser("Browser targets", uuid.NewString()+"@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	other, err := database.CreateUser("Other device owner", uuid.NewString()+"@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	document, key := sdkInstallFixture(t, "example.mail")
	document.Scopes = []string{"capabilities.providers.write", "capabilities.read", "browser.inspect", "inbox.read"}
	provider := &document.Capabilities.Providers[0]
	provider.ID = "example.mail/browser"
	provider.Route = cap.Route{Kind: "browser", Origins: []string{"https://mail.google.com", "https://outlook.live.com"}, Hints: []string{}}
	raw, err := os.ReadFile("../../../internal/capabilities/builtins.json")
	if err != nil {
		t.Fatal(err)
	}
	var builtins []cap.Definition
	if err := json.Unmarshal(raw, &builtins); err != nil {
		t.Fatal(err)
	}
	for _, definition := range builtins {
		if definition.Name == "inbox.read" {
			provider.Capabilities[0] = definition
			break
		}
	}
	if provider.Capabilities[0].Name != "inbox.read" {
		t.Fatal("missing canonical inbox.read fixture")
	}
	signed, digest := sdkSignFixture(t, document, key)
	if _, err := database.InstallVerifiedSDKApp(ctx, owner.ID, signed, digest); err != nil {
		t.Fatal(err)
	}
	session, err := database.CreateAppRuntimeSession(ctx, owner.ID, document.AppID, security.HashToken("browser-target-provider"), "", AppRuntimeSessionTTL)
	if err != nil {
		t.Fatal(err)
	}
	appctx := WithAppExecutionAuthority(ctx, *session)
	if _, err := database.RegisterSDKProvider(appctx, owner.ID, digest, *provider); err != nil {
		t.Fatal(err)
	}
	// A provider report cannot enable an adapter that the host has not implemented.
	if err := database.ReportSDKProviderAvailability(appctx, owner.ID, provider.ID, cap.Availability{State: "available", ObservedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	deviceFor := func(user string) *TrustedDevice {
		t.Helper()
		public, _, _ := ed25519.GenerateKey(rand.Reader)
		device, err := database.RegisterTrustedDevice(user, "Target Mac", base64.RawURLEncoding.EncodeToString(public), "macos", "", json.RawMessage(`[]`), json.RawMessage(`{"browser_tools":true}`))
		if err != nil {
			t.Fatal(err)
		}
		return device
	}
	device := deviceFor(owner.ID)
	foreign := deviceFor(other.ID)
	binding := cap.BrowserBinding{Kind: "browser", DeviceID: device.ID, ProfileID: strings.Repeat("a", 64), AccountBindingID: uuid.NewString(), Origins: []string{"https://mail.google.com"}, ContextID: uuid.NewString()}
	request := cap.TargetConfiguration{TargetID: uuid.NewString(), ProviderID: provider.ID, ProviderVersion: 1, Label: "Personal inbox", Capabilities: []string{"inbox.read"}, CallerApps: []string{}, Browser: &binding}
	target, err := database.ConfigureSDKTarget(ctx, owner.ID, request)
	if err != nil {
		t.Fatal(err)
	}

	page, err := database.SDKTargetsForControl(ctx, owner.ID, "", 1)
	if err != nil || len(page.Targets) != 1 || page.Targets[0].Target.ID != target.ID || !page.Targets[0].Enabled {
		t.Fatalf("trusted inventory: %#v %v", page, err)
	}
	if _, err := database.SDKTargetsForControl(appctx, owner.ID, "", 50); !errors.Is(err, ErrAppRuntimeForbidden) {
		t.Fatalf("app read user grants: %v", err)
	}
	page, err = database.SDKTargetsForControl(ctx, other.ID, "", 50)
	if err != nil || len(page.Targets) != 0 {
		t.Fatalf("cross-user inventory: %#v %v", page, err)
	}
	if _, err := target.ValidateBrowser(*provider); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ConfigureSDKTarget(appctx, owner.ID, request); !errors.Is(err, ErrAppRuntimeForbidden) {
		t.Fatalf("app self-bound target: %v", err)
	}
	if _, err := database.ConfigureSDKTarget(ctx, owner.ID, request); !errors.Is(err, ErrSDKVersionConflict) {
		t.Fatalf("stale target update: %v", err)
	}
	if _, err := database.ResolveSDKBoundCapability(ctx, owner.ID, target.ID, 1, "inbox.read", 1); !errors.Is(err, ErrSDKProviderUnavailable) {
		t.Fatalf("browser fell through to backend: %v", err)
	}
	request.ExpectedRevision = 1
	binding.DeviceID = foreign.ID
	if _, err := database.ConfigureSDKTarget(ctx, owner.ID, request); !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("foreign device bound: %v", err)
	}
	binding.DeviceID = device.ID
	binding.Origins = []string{"https://mail.google.com.evil.invalid"}
	if _, err := database.ConfigureSDKTarget(ctx, owner.ID, request); !errors.Is(err, cap.ErrInvalid) {
		t.Fatalf("expanded origin: %v", err)
	}
	binding.Origins = []string{"https://outlook.live.com"}
	binding.ProfileID = strings.Repeat("b", 64)
	binding.AccountBindingID = uuid.NewString()
	updated, err := database.ConfigureSDKTarget(ctx, owner.ID, request)
	if err != nil || updated.Revision != 2 {
		t.Fatalf("update: %#v %v", updated, err)
	}
	// Previous profile/account checkpoints are immutable and never use a backend secret.
	err = database.TestingWithRLSContext(ctx, map[string]string{"app.current_user_id": owner.ID, "app.rls_mode": "user"}, func(tx *sql.Tx) error {
		var raw []byte
		var noConnection bool
		if err := tx.QueryRowContext(ctx, `SELECT target,connection_id IS NULL AND connection_revision IS NULL FROM sdk_target_versions WHERE user_id=$1 AND id=$2 AND revision=1`, owner.ID, target.ID).Scan(&raw, &noConnection); err != nil {
			return err
		}
		var old cap.Target
		if err := json.Unmarshal(raw, &old); err != nil {
			return err
		}
		oldBinding, err := old.ValidateBrowser(*provider)
		if err != nil || oldBinding.ProfileID != strings.Repeat("a", 64) || !noConnection {
			t.Fatalf("old target changed or retained backend: %#v %v %v", oldBinding, noConnection, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.RevokeTrustedDevice(owner.ID, device.ID); err != nil {
		t.Fatal(err)
	}
	request.ExpectedRevision = 2
	if _, err := database.ConfigureSDKTarget(ctx, owner.ID, request); !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("revoked device rebound: %v", err)
	}
	targets, err := database.ResolveSDKTargets(ctx, owner.ID, cap.TargetResolve{Capability: "inbox.read"})
	if err != nil || len(targets) != 0 {
		t.Fatalf("unavailable target discovery: %#v %v", targets, err)
	}
}

func TestSDKTargetControlPagesIncludeDisabledTargets(t *testing.T) {
	database, _, user, request := sdkInvocationFixture(t)
	ctx := t.Context() // Host settings own target configuration; app sessions cannot change it.
	original := request.TargetID
	config := cap.TargetConfiguration{TargetID: uuid.NewString(), ProviderID: request.ProviderID, ProviderVersion: 1, Label: "Second target", Capabilities: []string{"habits.list"}, CallerApps: []string{}}
	if _, err := database.ConfigureSDKTarget(ctx, user, config); err != nil {
		t.Fatal(err)
	}
	if err := database.RevokeSDKTarget(ctx, user, original); err != nil {
		t.Fatal(err)
	}
	first, err := database.SDKTargetsForControl(ctx, user, "", 1)
	if err != nil || len(first.Targets) != 1 || first.NextCursor == nil {
		t.Fatalf("first page: %#v %v", first, err)
	}
	second, err := database.SDKTargetsForControl(ctx, user, *first.NextCursor, 1)
	if err != nil || len(second.Targets) != 1 || second.NextCursor != nil || second.Targets[0].Target.ID == first.Targets[0].Target.ID {
		t.Fatalf("second page: %#v %v", second, err)
	}
	for _, item := range append(first.Targets, second.Targets...) {
		if item.Target.ID == original && item.Enabled {
			t.Fatal("disabled target hidden or enabled")
		}
	}
}

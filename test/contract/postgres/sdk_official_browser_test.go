package db

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestSDKOfficialBrowserProviderAdmission(t *testing.T) {
	for _, id := range []string{"inbox/gmail", "inbox/outlook", "planner/todoist"} {
		t.Run(id, func(t *testing.T) {
			database := openTestDatabase(t)
			ctx := t.Context()
			user, err := database.CreateUser("Browser pilot", uuid.NewString()+"@example.com", "password123")
			if err != nil {
				t.Fatal(err)
			}
			p, _ := cap.OfficialBrowserProvider(id, 1)
			public, _, _ := ed25519.GenerateKey(rand.Reader)
			device, err := database.RegisterTrustedDevice(user.ID, "Pilot Mac", base64.RawURLEncoding.EncodeToString(public), "macos", "", json.RawMessage(`[]`), json.RawMessage(`{"browser_tools":true}`))
			if err != nil {
				t.Fatal(err)
			}
			request := cap.TargetConfiguration{TargetID: uuid.NewString(), ProviderID: id, ProviderVersion: 1, Label: p.Label + " pilot", CallerApps: []string{}, Capabilities: []string{}, Browser: &cap.BrowserBinding{Kind: "browser", DeviceID: device.ID, ProfileID: strings.Repeat("a", 64), AccountBindingID: uuid.NewString(), Origins: p.Route.Origins, ScopeID: "pilot-scope", AccountIdentity: "pilot@example.com"}}
			for _, d := range p.Capabilities {
				request.Capabilities = append(request.Capabilities, d.Name)
			}
			target, err := database.ConfigureSDKTarget(ctx, user.ID, request)
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range request.Capabilities {
				bound, err := database.ResolveSDKBoundCapability(ctx, user.ID, target.ID, 0, name, 1)
				if err != nil || bound.Provider.Route.Kind != "browser" || bound.Provider.Route.Adapter != p.Route.Adapter {
					t.Fatalf("%s did not resolve shipped browser adapter: %v", name, err)
				}
			}
			request.ExpectedRevision = target.Revision
			request.Browser.Origins = []string{"https://unrelated.example.com"}
			if _, err := database.ConfigureSDKTarget(ctx, user.ID, request); err == nil {
				t.Fatal("unrelated origin admitted")
			}
			if err := database.RevokeSDKTarget(ctx, user.ID, target.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := database.ResolveSDKBoundCapability(ctx, user.ID, target.ID, 0, request.Capabilities[0], 1); err == nil {
				t.Fatal("revoked target resolved")
			}
			request.Browser.Origins = p.Route.Origins
			if _, err := database.ConfigureSDKTarget(ctx, user.ID, request); !errors.Is(err, db.ErrSDKVersionConflict) {
				t.Fatalf("stale setup re-enabled a revoked target: %v", err)
			}
			if err := database.RevokeSDKTarget(ctx, user.ID, target.ID); err != nil {
				t.Fatal(err)
			}
			page, err := database.SDKTargetsForControl(ctx, user.ID, "", 100)
			if err != nil || len(page.Targets) != 1 || page.Targets[0].Enabled || page.Targets[0].Target.Revision != target.Revision+1 {
				t.Fatalf("revocation was not idempotent and versioned: %#v %v", page, err)
			}
		})
	}
}

package capabilities

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBrowserTargetBindsCanonicalOriginsAndRealDeviceIdentities(t *testing.T) {
	provider := Provider{ID: "example.mail/browser", Version: 1, Route: Route{Kind: "browser", Origins: []string{"https://mail.google.com", "https://outlook.live.com"}}}
	binding := BrowserBinding{Kind: "browser", DeviceID: "device_10000000-0000-4000-8000-000000000001", ProfileID: strings.Repeat("a", 64), AccountBindingID: "10000000-0000-4000-8000-000000000002", Origins: []string{"https://mail.google.com"}}
	target := Target{ID: "10000000-0000-4000-8000-000000000003", Revision: 1, AppID: "example.mail", ProviderID: provider.ID, ProviderVersion: 1, Label: "Personal mail"}
	check := func(b BrowserBinding) error {
		target.Binding, _ = json.Marshal(b)
		_, err := target.ValidateBrowser(provider)
		return err
	}
	if err := check(binding); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*BrowserBinding){
		"other origin":      func(b *BrowserBinding) { b.Origins = []string{"https://evil.invalid"} },
		"duplicate origins": func(b *BrowserBinding) { b.Origins = append(b.Origins, b.Origins[0]) },
		"profile path":      func(b *BrowserBinding) { b.ProfileID = "/Users/person/Profile" },
		"wrong kind":        func(b *BrowserBinding) { b.Kind = "backend" },
		"invented device":   func(b *BrowserBinding) { b.DeviceID = "device_other" },
		"blank account":     func(b *BrowserBinding) { b.AccountBindingID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			b := binding
			b.Origins = append([]string{}, binding.Origins...)
			change(&b)
			if check(b) == nil {
				t.Fatal("invalid binding accepted")
			}
		})
	}
	for _, origin := range []string{"https://mail.google.com/", "https://mail.google.com?", "https://mail.google.com:443", "https://user@mail.google.com", "http://mail.google.com", "https://MAIL.google.com", "https://mail.google.com/path"} {
		provider.Route.Origins = []string{origin}
		b := binding
		b.Origins = []string{origin}
		if check(b) == nil {
			t.Fatalf("noncanonical origin accepted: %s", origin)
		}
	}
	provider.Route.Origins = []string{"https://mail.google.com"}
	target.AppID = "another.app"
	if check(binding) == nil {
		t.Fatal("provider owner substitution accepted")
	}
}

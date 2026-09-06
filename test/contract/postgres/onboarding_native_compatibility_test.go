package db

import (
	"encoding/json"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
	"os"
	"reflect"
	"testing"
)

func TestNativeOnboardingFingerprintAndTemplateCompatibility(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/migration/fixtures/onboarding-fingerprints.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name        string           `json:"name"`
		Apps        []AppInstallSpec `json:"apps"`
		Fingerprint string           `json:"fingerprint"`
	}
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		actual, err := TestingOnboardingFingerprint(fixture.Name, fixture.Apps)
		if err != nil {
			t.Fatal(err)
		}
		if actual != fixture.Fingerprint {
			t.Fatalf("native onboarding retry fingerprint differs for %q", fixture.Name)
		}
	}
	raw, err = os.ReadFile("../../../docs/migration/fixtures/space-templates.json")
	if err != nil {
		t.Fatal(err)
	}
	var templates []SpaceTemplate
	if err := json.Unmarshal(raw, &templates); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(BuiltInSpaceTemplates(), templates) {
		t.Fatal("native built-in Space template catalog differs")
	}
}

package capabilities

import (
	"encoding/json"
	"os"
	"testing"
)

func TestRoutineSDKConformance(t *testing.T) {
	raw, err := os.ReadFile("routine-conformance.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name       string
		Input      json.RawMessage
		Accepted   bool
		Normalized json.RawMessage
	}
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			_, normalized, err := ParseRoutineDefinition(fixture.Input)
			if (err == nil) != fixture.Accepted {
				t.Fatalf("SDK accepted=%v; Go error=%v", fixture.Accepted, err)
			}
			if fixture.Accepted && !EqualJSON(normalized, fixture.Normalized) {
				t.Fatalf("normalization drift: %s", normalized)
			}
		})
	}
}
func TestRoutineRejectsDuplicateKeysAndDeepJSON(t *testing.T) {
	for _, raw := range []string{`{"protocol":1,"protocol":2}`, `{"constructor":{}}`} {
		if _, _, err := ParseRoutineDefinition([]byte(raw)); err == nil {
			t.Fatal("unsafe JSON accepted")
		}
	}
}

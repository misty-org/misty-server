package db

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestNormalizePersonalAgentRequiresConcreteModel(t *testing.T) {
	tests := []struct {
		name  string
		agent PersonalAgent
		valid bool
	}{
		{
			name: "pinned model",
			agent: PersonalAgent{
				Name:      "Researcher",
				ModelMode: "pinned",
				ModelID:   "google/gemini-2.5-flash-lite",
			},
			valid: true,
		},
		{name: "missing model", agent: PersonalAgent{Name: "Researcher"}},
		{
			name: "automatic mode",
			agent: PersonalAgent{
				Name:      "Researcher",
				ModelMode: "automatic",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := TestingNormalizePersonalAgent(&test.agent)
			if test.valid && err != nil {
				t.Fatalf("normalizePersonalAgent() error = %v", err)
			}
			if !test.valid && !errors.Is(err, ErrSpaceInvalid) {
				t.Fatalf("normalizePersonalAgent() error = %v, want ErrSpaceInvalid", err)
			}
		})
	}
}

func TestNormalizePersonalAgentAvatarSupportsLegacyPresetAndImmutableUpload(t *testing.T) {
	legacy, err := TestingNormalizePersonalAgentAvatar(nil, "researcher")
	if err != nil || !strings.Contains(string(legacy), `"preset_id":"researcher"`) {
		t.Fatalf("legacy avatar = %s, %v", legacy, err)
	}
	upload, err := TestingNormalizePersonalAgentAvatar(json.RawMessage(`{"kind":"upload","asset_id":"agent-avatar_personal_123_v2","version":2}`), "")
	if err != nil || !strings.Contains(string(upload), `"version":2`) {
		t.Fatalf("uploaded avatar = %s, %v", upload, err)
	}
	for _, invalid := range []json.RawMessage{
		json.RawMessage(`{"kind":"upload","asset_id":"avatars/user","version":1}`),
		json.RawMessage(`{"kind":"upload","asset_id":"agent-avatar_missing-version"}`),
		json.RawMessage(`{"kind":"unknown"}`),
	} {
		if _, err := TestingNormalizePersonalAgentAvatar(invalid, ""); !errors.Is(err, ErrSpaceInvalid) {
			t.Fatalf("avatar %s error = %v, want ErrSpaceInvalid", invalid, err)
		}
	}
}

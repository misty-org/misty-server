package db

import (
	"encoding/json"
	"testing"
)

func TestShellAndFolderWritesFollowTheirGrantKinds(t *testing.T) {
	cases := []struct {
		kind         string
		capabilities []string
		allowed      bool
	}{
		{"workspace", []string{"shell.run"}, true},
		{"local_folder", []string{"folder.read", "folder.write"}, true},
		// A folder grant can't carry the shell, and the workspace grant can't write to a folder.
		{"local_folder", []string{"shell.run"}, false},
		{"workspace", []string{"folder.write"}, false},
		// Deleting from a shared folder is never something a grant can ask for.
		{"local_folder", []string{"folder.delete"}, false},
		{"local_folder", []string{"folder.move"}, false},
	}
	for _, test := range cases {
		raw, _ := json.Marshal(test.capabilities)
		if got := DeviceContextCapabilitiesAllowed(test.kind, raw); got != test.allowed {
			t.Errorf("%s %v = %v, want %v", test.kind, test.capabilities, got, test.allowed)
		}
	}
}

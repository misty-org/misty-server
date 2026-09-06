package appcatalog_test

import (
	"testing"

	. "github.com/kannachi323/misty/server/internal/appcatalog"
)

func TestOfficialCatalogHasStableUniqueApps(t *testing.T) {
	apps := All()
	if len(apps) != 10 {
		t.Fatalf("All() returned %d apps, want 10", len(apps))
	}
	seen := map[string]bool{}
	for _, item := range apps {
		if item.ID == "" || item.Name == "" || item.Publisher != "Misty" || !item.Official {
			t.Fatalf("invalid official app: %#v", item)
		}
		if seen[item.ID] {
			t.Fatalf("duplicate app id %q", item.ID)
		}
		seen[item.ID] = true
		if item.Desktop.Runtime != RuntimeDownloaded || item.Version == "" || item.PermissionVersion < 2 || item.MinimumHost != 2 || item.Desktop.Entry == "" || len(item.Desktop.SHA256) != 64 || item.Desktop.Signature == "" || item.Desktop.SignatureKeyID == "" {
			t.Fatalf("%s must carry its signed download and reviewed permissions: %#v", item.ID, item)
		}
	}
	for _, desktopOnly := range []string{"code", "terminal"} {
		item, ok := Find(desktopOnly)
		if !ok || item.Mobile.Runtime != RuntimeUnsupported {
			t.Fatalf("%s mobile runtime = %#v, want unsupported", desktopOnly, item.Mobile)
		}
	}
	for _, embedded := range []string{"chat", "journal", "planner", "library", "inbox", "agents", "files", "browser"} {
		item, ok := Find(embedded)
		if !ok || item.Mobile.Runtime != RuntimeEmbedded || item.Mobile.Entry != "" || item.Mobile.SHA256 != "" || item.Mobile.StyleSHA256 != "" {
			t.Fatalf("%s mobile app is not Host-embedded: %#v", embedded, item.Mobile)
		}
	}
	if _, ok := Find("transfers"); ok {
		t.Fatal("Transfers must remain a Files subsection, not an official app")
	}
}

func TestNormalizeIDsRejectsUnknownAndDuplicateApps(t *testing.T) {
	if _, ok := NormalizeIDs([]string{"chat", "unknown"}); ok {
		t.Fatal("unknown app accepted")
	}
	if _, ok := NormalizeIDs([]string{"chat", "chat"}); ok {
		t.Fatal("duplicate app accepted")
	}
	apps, ok := NormalizeIDs([]string{})
	if !ok || len(apps) != 0 {
		t.Fatalf("empty selection = %#v, %v", apps, ok)
	}
}

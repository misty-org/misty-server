package api

import (
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

func TestOpaqueSmartLibraryIDsRejectPaths(t *testing.T) {
	if !TestingValidOpaqueID("asset_123", "asset_") {
		t.Fatal("valid opaque ID rejected")
	}
	for _, value := range []string{"asset_/Users/photo.jpg", "asset_hello world", "other_123"} {
		if TestingValidOpaqueID(value, "asset_") {
			t.Fatalf("accepted %q", value)
		}
	}
}

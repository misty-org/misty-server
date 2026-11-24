package api

import (
	"testing"

	. "github.com/misty-org/misty-server/internal/platform/httpapi"
)

func TestRateLimitPathsFoldAppAndRoomIDs(t *testing.T) {
	cases := map[string]string{
		"/v1/spaces/space_abcdefabcdefabcdef/apps/sketch/rooms/board/collaboration-ticket": "/spaces/{spaceID}/apps/{id}/rooms/{id}/collaboration-ticket",
		"/spaces/space_abcdefabcdefabcdef/apps/sketch/rooms/a.b:c/collaboration-ticket":    "/spaces/{spaceID}/apps/{id}/rooms/{id}/collaboration-ticket",
		"/spaces/space_abcdefabcdefabcdef/apps/sketch":                                     "/spaces/{spaceID}/apps/{id}",
	}
	for input, want := range cases {
		if got := NormalizeRateLimitPath(input); got != want {
			t.Fatalf("NormalizeRateLimitPath(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSpaceAppsAcceptTheCallsPermission(t *testing.T) {
	f := newDriveFixture(t, "calls-permission")
	app, err := f.database.InstallSpaceApp(f.ctx, f.ownerID, f.spaceID, "huddle", "1.0.0", []string{"calls", "rooms"}, nil)
	if err != nil {
		t.Fatalf("install with calls = %v", err)
	}
	if len(app.Permissions) != 2 || app.Permissions[0] != "calls" || app.Permissions[1] != "rooms" {
		t.Fatalf("permissions = %v, want calls and rooms", app.Permissions)
	}
}

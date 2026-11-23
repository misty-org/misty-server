package api

import "testing"

func TestOnlyAnAppsOwnIdsAndRoomsFoldIntoOneRoute(t *testing.T) {
	cases := map[string]string{
		"/spaces/space_abcdefabcdefabcdef/apps/sketch":                                  "/spaces/{spaceID}/apps/{id}",
		"/spaces/space_abcdefabcdefabcdef/apps/sketch/rooms/board/collaboration-ticket": "/spaces/{spaceID}/apps/{id}/rooms/{id}/collaboration-ticket",
		"/spaces/space_abcdefabcdefabcdef/apps":                                         "/spaces/{spaceID}/apps",
		"/spaces/space_abcdefabcdefabcdef/voice/rooms/lobby":                            "/spaces/{spaceID}/voice/rooms/lobby",
		"/spaces/space_abcdefabcdefabcdef/rooms/lobby":                                  "/spaces/{spaceID}/rooms/lobby",
		"/spaces/space_abcdefabcdefabcdef/drive/apps/sketch":                            "/spaces/{spaceID}/drive/apps/sketch",
	}
	for input, want := range cases {
		if got := NormalizeRateLimitPath(input); got != want {
			t.Fatalf("NormalizeRateLimitPath(%q) = %q, want %q", input, got, want)
		}
	}
}

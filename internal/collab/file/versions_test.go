package file

import "testing"

func TestRaisesABaseOnlyToAVersionWhoseContentIsTheDrivesCurrentContent(t *testing.T) {
	drive := Versions{Version: 7, ContentVersion: 5}
	cases := []struct {
		version int64
		drive   Versions
		want    bool
	}{
		{5, drive, true}, {7, drive, true}, {4, drive, false}, {8, drive, false},
		{7, Versions{Version: 7}, true}, {9, Versions{Version: 7}, false},
	}
	for _, c := range cases {
		if got := ClaimHolds(ClaimBase, c.version, c.drive, 0); got != c.want {
			t.Errorf("base %d on %+v = %v", c.version, c.drive, got)
		}
	}
}

func TestTakesAKeptCopyOnlyForAVersionTheDriveReachedWithContentTheRoomNeverSaved(t *testing.T) {
	drive := Versions{Version: 7, ContentVersion: 5}
	cases := []struct {
		claim   Claim
		version int64
		drive   Versions
		saved   int64
		want    bool
	}{
		{ClaimKept, 7, drive, 4, true}, {ClaimKept, 6, drive, 4, true}, {ClaimKept, 8, drive, 4, false},
		{ClaimKept, 7, drive, 5, false}, {ClaimKept, 7, Versions{Version: 7}, 6, true}, {ClaimKept, 7, Versions{Version: 7}, 7, false},
		{ClaimOutside, 7, drive, 4, true}, {ClaimOutside, 8, drive, 4, false}, {ClaimOutside, 7, drive, 5, false},
	}
	for _, c := range cases {
		if got := ClaimHolds(c.claim, c.version, c.drive, c.saved); got != c.want {
			t.Errorf("%s %d on %+v saved %d = %v", c.claim, c.version, c.drive, c.saved, got)
		}
	}
}

func TestRaisesABaseForTheDrivesCurrentContentOnlyFromThePersonWhoWroteIt(t *testing.T) {
	written := Versions{Version: 7, ContentVersion: 7, ModifiedBy: "user-b"}
	renamed := Versions{Version: 8, ContentVersion: 7, ModifiedBy: "user-b"}
	cases := []struct {
		drive  Versions
		user   string
		writer bool
		want   bool
	}{
		{written, "user-b", false, true}, {written, "user-b", true, true}, {written, "user-c", false, false},
		{written, "user-c", true, false}, {written, "", false, false},
		{renamed, "user-b", false, false}, {renamed, "user-c", true, true},
		{Versions{Version: 7, ContentVersion: 7}, "user-b", false, false}, {Versions{Version: 7, ContentVersion: 7}, "user-b", true, true},
		{Versions{Version: 7, ModifiedBy: "user-b"}, "user-b", false, false}, {Versions{Version: 7, ModifiedBy: "user-b"}, "user-b", true, true},
	}
	for _, c := range cases {
		if got := BaseClaimantHolds(c.drive, c.user, c.writer); got != c.want {
			t.Errorf("%+v %q writer=%v = %v", c.drive, c.user, c.writer, got)
		}
	}
}

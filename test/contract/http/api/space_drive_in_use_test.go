package api

import (
	"net/http"
	"testing"
)

func TestDriveTrashIsRefusedWhileInUse(t *testing.T) {
	f := newDriveFixture(t, "drive-in-use")
	box := f.folder(f.owner, nil, "Shared")
	report := f.file(f.owner, f.ownerID, &box.ID, "Report.pdf", "open on Ben's laptop")

	lease := driveJSON[map[string]any](t, f.call(f.member, http.MethodPut, f.itemPath(report.ID)+"/in-use", 0, map[string]any{"editing": false}), http.StatusOK)
	if lease["expires_at"] == nil {
		t.Fatalf("lease = %v", lease)
	}
	for _, target := range []driveTestItem{report, box} {
		refused := driveJSON[map[string]any](t, f.call(f.owner, http.MethodPost, f.itemPath(target.ID)+"/trash", target.Version, nil), http.StatusLocked)
		who, _ := refused["who"].([]any)
		if refused["code"] != "in_use" || len(who) != 1 {
			t.Fatalf("trash of %s = %v", target.Name, refused)
		}
		holder := who[0].(map[string]any)
		if holder["user_id"] != f.memberID || holder["device_name"] != f.member.name || holder["editing"] != false {
			t.Fatalf("who = %v", holder)
		}
	}
	renamed := driveJSON[driveTestItem](t, f.call(f.owner, http.MethodPatch, f.itemPath(report.ID), report.Version, map[string]any{"name": "Report v2.pdf"}), http.StatusOK)
	driveJSON[map[string]any](t, f.call(f.owner, http.MethodPut, f.itemPath(renamed.ID)+"/in-use", 0, map[string]any{"editing": true}), http.StatusOK)
	if wait := driveJSON[map[string]any](t, f.call(f.owner, http.MethodPost, f.itemPath(box.ID)+"/check-in-use", 0, nil), http.StatusOK); wait["wait_ms"] != float64(2000) {
		t.Fatalf("check-in-use = %v", wait)
	}
	if event := f.text(`SELECT event_type FROM space_events WHERE space_id=$1 ORDER BY id DESC LIMIT 1`, f.spaceID); event != "drive.in_use_check" {
		t.Fatalf("last event = %s", event)
	}

	f.exec(`UPDATE space_drive_in_use SET expires_at=NOW()-interval '1 second' WHERE user_id=$1`, f.memberID)
	trashed := driveJSON[driveTestItem](t, f.call(f.owner, http.MethodPost, f.itemPath(box.ID)+"/trash", box.Version, nil), http.StatusOK)
	if !trashed.Trashed {
		t.Fatalf("trash after the lease expired = %+v", trashed)
	}
	f.exec(`UPDATE space_drive_in_use SET expires_at=NOW()+interval '2 minutes' WHERE user_id=$1`, f.memberID)
	restored := driveJSON[driveTestItem](t, f.call(f.owner, http.MethodPost, f.itemPath(box.ID)+"/restore", 0, nil), http.StatusOK)
	if got := f.call(f.member, http.MethodDelete, f.itemPath(renamed.ID)+"/in-use", 0, nil); got.Code != http.StatusNoContent {
		t.Fatalf("clear = %d %s", got.Code, got.Body.String())
	}
	driveJSON[driveTestItem](t, f.call(f.owner, http.MethodPost, f.itemPath(box.ID)+"/trash", restored.Version, nil), http.StatusOK)
}

func TestDriveOnePersonEditsOnManyComputers(t *testing.T) {
	f := newDriveFixture(t, "drive-editing")
	doc := f.file(f.owner, f.ownerID, nil, "Plan.md", "# Plan")
	path := f.itemPath(doc.ID)

	driveJSON[map[string]any](t, f.call(f.member, http.MethodPut, path+"/in-use", 0, map[string]any{"editing": true}), http.StatusOK)
	driveJSON[map[string]any](t, f.call(f.memberB, http.MethodPut, path+"/in-use", 0, map[string]any{"editing": true}), http.StatusOK)
	marks := driveJSON[struct {
		Marks []driveTestMark `json:"marks"`
	}](t, f.call(f.owner, http.MethodGet, f.base+"/in-use", 0, nil), http.StatusOK).Marks
	if len(marks) != 2 || !marks[0].Editing || !marks[1].Editing {
		t.Fatalf("marks with two editing computers = %+v", marks)
	}
	first := driveJSON[driveTestItem](t, f.call(f.member, http.MethodPatch, path, 0, map[string]any{"mtime_ms": 1}), http.StatusOK)
	second := driveJSON[driveTestItem](t, f.call(f.memberB, http.MethodPatch, path, 0, map[string]any{"mtime_ms": 2}), http.StatusOK)
	if second.Version != first.Version+1 || second.MTimeMS != 2 {
		t.Fatalf("second computer's change = %+v", second)
	}
	driveJSON[driveTestItem](t, f.call(f.owner, http.MethodPatch, path, second.Version, map[string]any{"mtime_ms": 3}), http.StatusOK)
	if res := f.call(f.memberB, http.MethodPost, path+"/edit-here", 0, nil); res.Code != http.StatusNotFound && res.Code != http.StatusMethodNotAllowed {
		t.Fatalf("edit-here = %d %s", res.Code, res.Body.String())
	}
	if n := f.text(`SELECT count(*)::text FROM space_events WHERE space_id=$1 AND event_type='drive.edit_moved'`, f.spaceID); n != "0" {
		t.Fatalf("drive.edit_moved events = %s", n)
	}
}

type driveTestMark struct {
	ItemID     string `json:"item_id"`
	UserID     string `json:"user_id"`
	Name       string `json:"name"`
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name"`
	Editing    bool   `json:"editing"`
	ExpiresAt  string `json:"expires_at"`
}

func TestDriveInUseMarksAreListedForEveryMember(t *testing.T) {
	f := newDriveFixture(t, "drive-in-use-list")
	doc := f.file(f.owner, f.ownerID, nil, "Notes.md", "# Notes")
	path := f.itemPath(doc.ID)
	marks := func(from driveTestDevice) []driveTestMark {
		t.Helper()
		return driveJSON[struct {
			Marks []driveTestMark `json:"marks"`
		}](t, f.call(from, http.MethodGet, f.base+"/in-use", 0, nil), http.StatusOK).Marks
	}
	heard := func() string {
		t.Helper()
		return f.text(`SELECT count(*)::text FROM space_events WHERE space_id=$1 AND event_type='drive.in_use' AND payload->>'item_id'=$2`, f.spaceID, doc.ID)
	}
	if got := marks(f.owner); len(got) != 0 {
		t.Fatalf("marks before anyone opened it = %+v", got)
	}

	driveJSON[map[string]any](t, f.call(f.member, http.MethodPut, path+"/in-use", 0, map[string]any{"editing": false}), http.StatusOK)
	got := marks(f.owner)
	memberName := f.text(`SELECT COALESCE(username,'') FROM users WHERE id=$1`, f.memberID)
	if len(got) != 1 || got[0].ItemID != doc.ID || got[0].UserID != f.memberID || got[0].Name != memberName ||
		got[0].DeviceID != f.member.id || got[0].DeviceName != f.member.name || got[0].Editing || got[0].ExpiresAt == "" {
		t.Fatalf("marks after Ben opened it = %+v", got)
	}
	if n := heard(); n != "1" {
		t.Fatalf("drive.in_use after a new mark = %s", n)
	}
	driveJSON[map[string]any](t, f.call(f.member, http.MethodPut, path+"/in-use", 0, map[string]any{"editing": false}), http.StatusOK)
	if n := heard(); n != "1" {
		t.Fatalf("drive.in_use after a plain renewal = %s", n)
	}
	driveJSON[map[string]any](t, f.call(f.member, http.MethodPut, path+"/in-use", 0, map[string]any{"editing": true}), http.StatusOK)
	if n := heard(); n != "2" {
		t.Fatalf("drive.in_use after starting to edit = %s", n)
	}
	driveJSON[map[string]any](t, f.call(f.memberB, http.MethodPut, path+"/in-use", 0, map[string]any{"editing": false}), http.StatusOK)
	if n := heard(); n != "3" {
		t.Fatalf("drive.in_use after a second computer opened it = %s", n)
	}
	got = marks(f.member)
	if len(got) != 2 {
		t.Fatalf("marks with two computers = %+v", got)
	}
	editing := map[string]bool{}
	for _, mark := range got {
		editing[mark.DeviceID] = mark.Editing
	}
	if !editing[f.member.id] || editing[f.memberB.id] {
		t.Fatalf("editing by computer = %v", editing)
	}
	driveJSON[map[string]any](t, f.call(f.memberB, http.MethodPut, path+"/in-use", 0, map[string]any{"editing": true}), http.StatusOK)
	if n := heard(); n != "4" {
		t.Fatalf("drive.in_use after a second computer started editing = %s", n)
	}
	if code := driveCode(t, f.call(f.outsider, http.MethodGet, f.base+"/in-use", 0, nil), http.StatusForbidden); code != "forbidden" {
		t.Fatalf("outsider marks code = %s", code)
	}

	f.exec(`UPDATE space_drive_in_use SET expires_at=NOW()-interval '1 second' WHERE device_id=$1`, f.memberB.id)
	if got := marks(f.owner); len(got) != 1 || got[0].DeviceID != f.member.id {
		t.Fatalf("marks after one lease ran out = %+v", got)
	}
	if res := f.call(f.memberB, http.MethodDelete, path+"/in-use", 0, nil); res.Code != http.StatusNoContent {
		t.Fatalf("clear an expired mark = %d %s", res.Code, res.Body.String())
	}
	if n := heard(); n != "4" {
		t.Fatalf("drive.in_use after clearing an expired mark = %s", n)
	}
	if res := f.call(f.member, http.MethodDelete, path+"/in-use", 0, nil); res.Code != http.StatusNoContent {
		t.Fatalf("clear = %d %s", res.Code, res.Body.String())
	}
	if n := heard(); n != "5" {
		t.Fatalf("drive.in_use after closing = %s", n)
	}
	if got := marks(f.owner); len(got) != 0 {
		t.Fatalf("marks after closing = %+v", got)
	}
}

func TestDriveTrashEmptiesItselfExceptHeldItems(t *testing.T) {
	f := newDriveFixture(t, "drive-retention")
	plain := f.folder(f.owner, nil, "Plain")
	held := f.folder(f.owner, nil, "Held")
	driveJSON[driveTestItem](t, f.call(f.owner, http.MethodPost, f.itemPath(plain.ID)+"/trash", plain.Version, map[string]any{"held": false}), http.StatusOK)
	if item := driveJSON[driveTestItem](t, f.call(f.owner, http.MethodPost, f.itemPath(held.ID)+"/trash", held.Version, map[string]any{"held": true}), http.StatusOK); !item.TrashHold {
		t.Fatalf("held trash = %+v", item)
	}
	f.exec(`UPDATE space_drive_items SET trashed_at=NOW()-interval '31 days' WHERE space_id=$1`, f.spaceID)
	if _, err := f.drive.PurgeExpiredTrash(f.ctx, 100); err != nil {
		t.Fatal(err)
	}
	if item := driveJSON[driveTestItem](t, f.call(f.owner, http.MethodGet, f.itemPath(plain.ID), 0, nil), http.StatusOK); !item.Deleted {
		t.Fatalf("expired trash = %+v", item)
	}
	if item := driveJSON[driveTestItem](t, f.call(f.owner, http.MethodGet, f.itemPath(held.ID), 0, nil), http.StatusOK); item.Deleted || !item.Trashed {
		t.Fatalf("held trash after the purge = %+v", item)
	}
}

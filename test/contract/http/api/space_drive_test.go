package api

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/misty-org/misty-server/internal/platform/db"
	. "github.com/misty-org/misty-server/internal/platform/httpapi"
)

func TestDriveCreateAndListChanges(t *testing.T) {
	f := newDriveFixture(t, "drive-create")
	docs := f.folder(f.owner, nil, "Docs")
	if docs.Kind != "folder" || docs.ParentID != nil || docs.Version != 1 || docs.Seq < 1 || docs.SHA256 != nil {
		t.Fatalf("folder = %+v", docs)
	}
	report := f.file(f.member, f.memberID, &docs.ID, "Report.pdf", "quarterly numbers")
	if report.ParentID == nil || *report.ParentID != docs.ID || report.Size != 17 || report.ModifiedBy != f.memberID || report.Seq <= docs.Seq {
		t.Fatalf("file = %+v", report)
	}
	empty := driveJSON[driveTestItem](t, f.call(f.owner, http.MethodPost, f.base+"/items", 0, map[string]any{
		"parent_id": nil, "name": "empty.txt", "kind": "file", "sha256": db.EmptyFileSHA256, "size": 0,
	}), http.StatusCreated)
	if answer := driveJSON[map[string]any](t, f.call(f.owner, http.MethodPost, f.base+"/uploads", 0,
		map[string]any{"sha256": *report.SHA256, "size": report.Size}), http.StatusOK); answer["present"] != true || answer["upload_id"] == "" {
		t.Fatalf("second upload of the same content = %v", answer)
	}

	all := f.changes(f.member, 0)
	if len(all.Items) != 3 || all.More || all.Head != empty.Seq || all.NextSince != all.Head {
		t.Fatalf("changes since 0 = %+v", all)
	}
	for i, want := range []string{docs.ID, report.ID, empty.ID} {
		if all.Items[i].ID != want {
			t.Fatalf("change %d = %s, want %s", i, all.Items[i].ID, want)
		}
	}
	first := driveJSON[driveTestChanges](t, f.call(f.member, http.MethodGet, f.base+"/changes?since=0&limit=1", 0, nil), http.StatusOK)
	if len(first.Items) != 1 || !first.More || first.NextSince != docs.Seq {
		t.Fatalf("first page = %+v", first)
	}
	rest := f.changes(f.member, first.NextSince)
	if len(rest.Items) != 2 || rest.Items[0].ID != report.ID {
		t.Fatalf("second page = %+v", rest)
	}
	if none := f.changes(f.member, all.Head); len(none.Items) != 0 || none.NextSince != all.Head {
		t.Fatalf("changes since head = %+v", none)
	}

	if code := driveCode(t, f.call(f.outsider, http.MethodGet, f.base+"/changes?since=0", 0, nil), http.StatusForbidden); code != "forbidden" {
		t.Fatalf("outsider changes code = %s", code)
	}
	noDevice := f.owner
	noDevice.id = ""
	if code := driveCode(t, f.call(noDevice, http.MethodPost, f.base+"/items", 0, map[string]any{"parent_id": nil, "name": "x", "kind": "folder"}), http.StatusBadRequest); code != "device_required" {
		t.Fatalf("create without a device code = %s", code)
	}
	missing := "drv_" + uuid.NewString()
	if code := driveCode(t, f.call(f.owner, http.MethodPost, f.base+"/items", 0, map[string]any{"parent_id": missing, "name": "x", "kind": "folder"}), http.StatusNotFound); code != "parent_missing" {
		t.Fatalf("create in a missing folder code = %s", code)
	}
	for _, name := range []string{"", ".", "..", "a/b", `a\b`, "a\x00b", "tab\there", strings.Repeat("n", 256)} {
		if code := driveCode(t, f.call(f.owner, http.MethodPost, f.base+"/items", 0, map[string]any{"parent_id": nil, "name": name, "kind": "folder"}), http.StatusBadRequest); code != "invalid_request" {
			t.Fatalf("name %q code = %s", name, code)
		}
	}
	taken := driveJSON[map[string]any](t, f.call(f.owner, http.MethodPost, f.base+"/items", 0,
		map[string]any{"parent_id": docs.ID, "name": "report.PDF", "kind": "folder"}), http.StatusConflict)
	if taken["code"] != "name_taken" || taken["item"].(map[string]any)["id"] != report.ID {
		t.Fatalf("case-insensitive clash = %v", taken)
	}
}

func TestDriveCreateWithClientIDIsIdempotent(t *testing.T) {
	f := newDriveFixture(t, "drive-ids")
	id := "drv_" + uuid.NewString()
	body := map[string]any{"id": id, "parent_id": nil, "name": "Plans", "kind": "folder"}
	created := driveJSON[driveTestItem](t, f.call(f.owner, http.MethodPost, f.base+"/items", 0, body), http.StatusCreated)
	if created.ID != id {
		t.Fatalf("created id = %s, want %s", created.ID, id)
	}
	again := driveJSON[driveTestItem](t, f.call(f.owner, http.MethodPost, f.base+"/items", 0, body), http.StatusOK)
	if again.ID != id || again.Version != created.Version || again.Seq != created.Seq {
		t.Fatalf("retried create = %+v, want %+v", again, created)
	}
	if head := f.changes(f.owner, 0).Head; head != created.Seq {
		t.Fatalf("a retry stamped a change: head = %d", head)
	}
	clash := map[string]any{"id": id, "parent_id": nil, "name": "Other plans", "kind": "folder"}
	if code := driveCode(t, f.call(f.owner, http.MethodPost, f.base+"/items", 0, clash), http.StatusConflict); code != "id_taken" {
		t.Fatalf("same id, other name code = %s", code)
	}
	otherOwner, err := f.database.CreateUser(uniqueTestEmail("drive-ids-other-owner"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	otherSpace, err := f.database.TestingCreateSpace(f.ctx, otherOwner.ID, "Drive drive-ids-other")
	if err != nil {
		t.Fatal(err)
	}
	otherDevice := driveTestDevice{token: newConversationTestBearerToken(t, f.database, otherOwner.ID), id: "dev_" + uuid.NewString(), name: "Cleo's laptop"}
	if code := driveCode(t, f.call(otherDevice, http.MethodPost, "/spaces/"+otherSpace.ID+"/drive/items", 0, body), http.StatusConflict); code != "id_taken" {
		t.Fatalf("an id from another Space code = %s", code)
	}
	for _, bad := range []string{"drv_not-a-uuid", "dev_" + uuid.NewString(), "drv_" + strings.ToUpper(uuid.NewString())} {
		if code := driveCode(t, f.call(f.owner, http.MethodPost, f.base+"/items", 0, map[string]any{"id": bad, "parent_id": nil, "name": "x", "kind": "folder"}), http.StatusBadRequest); code != "invalid_request" {
			t.Fatalf("id %q code = %s", bad, code)
		}
	}
}

func TestDrivePatchIfMatchIsOptional(t *testing.T) {
	f := newDriveFixture(t, "drive-patch")
	docs := f.folder(f.owner, nil, "Docs")
	notes := f.file(f.owner, f.ownerID, nil, "notes.txt", "first")
	path := f.itemPath(notes.ID)

	renamed := driveJSON[driveTestItem](t, f.call(f.owner, http.MethodPatch, path, 0, map[string]any{"name": "renamed.txt"}), http.StatusOK)
	if renamed.Version != notes.Version+1 || renamed.Name != "renamed.txt" || renamed.Replaced != nil {
		t.Fatalf("unconditional rename = %+v", renamed)
	}
	moved := driveJSON[driveTestItem](t, f.call(f.owner, http.MethodPatch, path, renamed.Version, map[string]any{"name": "Notes.txt", "parent_id": docs.ID}), http.StatusOK)
	if moved.Version != renamed.Version+1 || moved.Name != "Notes.txt" || moved.ParentID == nil || *moved.ParentID != docs.ID || moved.Seq <= renamed.Seq {
		t.Fatalf("moved = %+v", moved)
	}
	stale := driveJSON[map[string]any](t, f.call(f.member, http.MethodPatch, path, notes.Version, map[string]any{"name": "mine.txt"}), http.StatusConflict)
	if stale["code"] != "version_conflict" || stale["item"].(map[string]any)["version"].(float64) != float64(moved.Version) {
		t.Fatalf("stale If-Match = %v", stale)
	}
	mine := driveJSON[driveTestItem](t, f.call(f.member, http.MethodPatch, path, 0, map[string]any{"name": "mine.txt"}), http.StatusOK)
	if mine.Name != "mine.txt" || mine.ModifiedBy != f.memberID {
		t.Fatalf("unconditional change from the member = %+v", mine)
	}
	uploadID, sha := f.upload(f.member, f.memberID, []byte("second draft"))
	changed := driveJSON[driveTestItem](t, f.call(f.member, http.MethodPatch, path, mine.Version,
		map[string]any{"sha256": sha, "size": 12, "upload_id": uploadID, "mtime_ms": 1800000000000}), http.StatusOK)
	if changed.SHA256 == nil || *changed.SHA256 != sha || changed.Size != 12 || changed.ModifiedBy != f.memberID || changed.MTimeMS != 1800000000000 || changed.Replaced != nil {
		t.Fatalf("changed content = %+v", changed)
	}
	back := driveJSON[driveTestItem](t, f.call(f.owner, http.MethodPatch, path, 0, map[string]any{"parent_id": nil}), http.StatusOK)
	if back.ParentID != nil {
		t.Fatalf("moved to the root = %+v", back)
	}
	if code := driveCode(t, f.call(f.owner, http.MethodPatch, f.itemPath(docs.ID), 0, map[string]any{"parent_id": docs.ID}), http.StatusConflict); code != "invalid_move" {
		t.Fatalf("folder into itself code = %s", code)
	}
	if code := driveCode(t, f.call(f.owner, http.MethodPatch, path, 0, map[string]any{"mtime_ms": 1, "base_version": 0}), http.StatusBadRequest); code != "invalid_request" {
		t.Fatalf("base_version 0 code = %s", code)
	}
}

func TestDriveStaleSaveKeepsReplacedContent(t *testing.T) {
	f := newDriveFixture(t, "drive-replaced")
	docs := f.folder(f.owner, nil, "Docs")
	notes := f.file(f.owner, f.ownerID, &docs.ID, "notes.txt", "first")
	path := f.itemPath(notes.ID)
	save := func(from driveTestDevice, userID, content string, base int64) driveTestItem {
		t.Helper()
		uploadID, sha := f.upload(from, userID, []byte(content))
		body := map[string]any{"sha256": sha, "size": len(content), "upload_id": uploadID}
		if base > 0 {
			body["base_version"] = base
		}
		return driveJSON[driveTestItem](t, f.call(from, http.MethodPatch, path, 0, body), http.StatusOK)
	}
	counted := func(itemID string) string {
		t.Helper()
		return f.text(`SELECT COALESCE(sum(logical_bytes),0)::text FROM space_storage_contributions
			WHERE space_id=$1 AND source_kind='drive' AND source_id=$2 AND state='active'`, f.spaceID, itemID)
	}

	ben := save(f.member, f.memberID, "ben's draft", notes.Version)
	if ben.Replaced != nil {
		t.Fatalf("save from the current version kept a copy: %+v", ben.Replaced)
	}
	benKey := f.text(`SELECT blob_key FROM space_drive_items WHERE id=$1`, notes.ID)
	renamed := driveJSON[driveTestItem](t, f.call(f.member, http.MethodPatch, path, 0, map[string]any{"name": "Notes.txt"}), http.StatusOK)
	before := f.changes(f.owner, 0).Head

	ana := save(f.owner, f.ownerID, "ana's draft", notes.Version)
	copy := ana.Replaced
	if copy == nil {
		t.Fatalf("stale save kept no copy: %+v", ana)
	}
	if ana.Name != "Notes.txt" || ana.ModifiedBy != f.ownerID || ana.Version != renamed.Version+1 {
		t.Fatalf("item after the stale save = %+v", ana)
	}
	if !regexp.MustCompile(`^Notes \(conflicted copy \d{4}-\d{2}-\d{2} \d{4}\)\.txt$`).MatchString(copy.Name) {
		t.Fatalf("copy name = %q", copy.Name)
	}
	if copy.TrashedBy != nil || copy.ReplacedOf == nil || *copy.ReplacedOf != notes.ID ||
		copy.Kind != "file" || copy.ParentID == nil || *copy.ParentID != docs.ID || copy.ModifiedBy != f.memberID ||
		copy.SHA256 == nil || *copy.SHA256 != *ben.SHA256 || copy.Size != ben.Size {
		t.Fatalf("replaced copy = %+v", copy)
	}
	if key := f.text(`SELECT blob_key FROM space_drive_items WHERE id=$1`, copy.ID); key != benKey {
		t.Fatalf("copy's blob = %s, want the replaced one %s", key, benKey)
	}
	after := f.changes(f.member, before)
	if len(after.Items) != 2 || after.Items[0].ID != copy.ID || after.Items[1].ID != notes.ID || after.Items[0].ReplacedOf == nil {
		t.Fatalf("changes after the stale save = %+v", after.Items)
	}
	if got := counted(copy.ID); got != strconv.FormatInt(ben.Size, 10) {
		t.Fatalf("copy counts %s bytes, want %d", got, ben.Size)
	}
	if got := counted(notes.ID); got != strconv.FormatInt(ana.Size, 10) {
		t.Fatalf("item counts %s bytes, want %d", got, ana.Size)
	}
	if trash := f.trashID(); trash != "" {
		t.Fatalf("a .trash folder %s, want the conflicted copy kept beside the file", trash)
	}

	blind := save(f.member, f.memberID, "blind save", 0)
	if blind.Replaced == nil || blind.Replaced.SHA256 == nil || *blind.Replaced.SHA256 != *ana.SHA256 || blind.Replaced.ID == copy.ID {
		t.Fatalf("save without a base = %+v", blind)
	}

	binned := driveJSON[driveTestItem](t, f.call(f.owner, http.MethodPost, f.itemPath(copy.ID)+"/trash", 0, nil), http.StatusOK)
	if !f.inTrash(binned) {
		t.Fatalf("trashed copy = %+v, want it in .trash", binned)
	}
	if got := counted(copy.ID); got != "0" {
		t.Fatalf("copy in .trash still counts %s bytes", got)
	}
	f.exec(`UPDATE space_drive_items SET trashed_at=NOW()-interval '31 days' WHERE id=$1`, copy.ID)
	if _, err := f.drive.PurgeExpiredTrash(f.ctx, 100); err != nil {
		t.Fatal(err)
	}
	if item := driveJSON[driveTestItem](t, f.call(f.owner, http.MethodGet, f.itemPath(copy.ID), 0, nil), http.StatusOK); !item.Deleted {
		t.Fatalf("expired copy = %+v", item)
	}
	if got := counted(copy.ID); got != "0" {
		t.Fatalf("expired copy still counts %s bytes", got)
	}
	if state := f.text(`SELECT lifecycle_state FROM library_blobs WHERE r2_object_key=$1`, benKey); state != "deleted" {
		t.Fatalf("expired copy's blob = %s, want deleted", state)
	}
}

func TestDriveTrashIsAFolderInTheDrive(t *testing.T) {
	f := newDriveFixture(t, "drive-trash")
	docs := f.folder(f.owner, nil, "Docs")
	report := f.file(f.owner, f.ownerID, &docs.ID, "Report.pdf", "the report")
	key := f.text(`SELECT blob_key FROM space_drive_items WHERE id=$1`, report.ID)
	counted := func(itemID string) string {
		t.Helper()
		return f.text(`SELECT COALESCE(sum(logical_bytes),0)::text FROM space_storage_contributions
			WHERE space_id=$1 AND source_kind='drive' AND source_id=$2 AND state='active'`, f.spaceID, itemID)
	}
	size := strconv.FormatInt(report.Size, 10)

	if code := driveCode(t, f.call(f.owner, http.MethodPost, f.itemPath(report.ID)+"/trash", report.Version+5, nil), http.StatusConflict); code != "version_conflict" {
		t.Fatalf("trash with a stale If-Match code = %s", code)
	}
	trashed := driveJSON[driveTestItem](t, f.call(f.member, http.MethodPost, f.itemPath(report.ID)+"/trash", 0, nil), http.StatusOK)
	if !f.inTrash(trashed) || trashed.Name != "Report.pdf" || trashed.TrashedBy == nil || *trashed.TrashedBy != f.memberID || trashed.Version != report.Version+1 {
		t.Fatalf("trashed = %+v", trashed)
	}
	if got := counted(report.ID); got != "0" {
		t.Fatalf("a file in .trash counts %s bytes", got)
	}
	if code := driveCode(t, f.call(f.owner, http.MethodPost, f.itemPath(report.ID)+"/trash", 0, nil), http.StatusConflict); code != "trashed" {
		t.Fatalf("trash of a trashed item code = %s", code)
	}
	if state := f.text(`SELECT lifecycle_state FROM library_blobs WHERE r2_object_key=$1`, key); state != "ready" {
		t.Fatalf("trashed file's blob = %s, want ready", state)
	}
	if _, err := f.store.Head(f.ctx, key); err != nil {
		t.Fatalf("trashed file's object: %v", err)
	}
	listed := false
	for _, item := range f.changes(f.member, 0).Items {
		if item.ID == trashed.ID && f.inTrash(item) {
			listed = true
		}
	}
	if !listed {
		t.Fatal("the trashed file isn't in the drive's changes under .trash")
	}

	twin := f.file(f.owner, f.ownerID, nil, "report.pdf", "another report")
	twin = driveJSON[driveTestItem](t, f.call(f.owner, http.MethodPost, f.itemPath(twin.ID)+"/trash", 0, nil), http.StatusOK)
	if !f.inTrash(twin) || twin.Name != "report 2.pdf" {
		t.Fatalf("second report in .trash = %+v", twin)
	}

	edited := driveJSON[driveTestItem](t, f.call(f.member, http.MethodPatch, f.itemPath(report.ID), 0, map[string]any{"mtime_ms": 5}), http.StatusOK)
	if !f.inTrash(edited) {
		t.Fatalf("an edit took the file out of .trash: %+v", edited)
	}

	restored := driveJSON[driveTestItem](t, f.call(f.member, http.MethodPost, f.itemPath(report.ID)+"/restore", 0, nil), http.StatusOK)
	if restored.ParentID == nil || *restored.ParentID != docs.ID || restored.Name != "Report.pdf" || restored.TrashedBy != nil {
		t.Fatalf("restored = %+v", restored)
	}
	if got := counted(report.ID); got != size {
		t.Fatalf("a restored file counts %s bytes, want %s", got, size)
	}
	if code := driveCode(t, f.call(f.member, http.MethodPost, f.itemPath(report.ID)+"/restore", 0, nil), http.StatusConflict); code != "not_trashed" {
		t.Fatalf("restore outside .trash code = %s", code)
	}

	trash := f.trashID()
	dropped := driveJSON[driveTestItem](t, f.call(f.member, http.MethodPatch, f.itemPath(report.ID), 0, map[string]any{"parent_id": trash}), http.StatusOK)
	if !f.inTrash(dropped) || dropped.TrashedBy == nil || counted(report.ID) != "0" {
		t.Fatalf("moved into .trash = %+v", dropped)
	}
	out := driveJSON[driveTestItem](t, f.call(f.member, http.MethodPatch, f.itemPath(report.ID), 0, map[string]any{"parent_id": nil}), http.StatusOK)
	if out.ParentID != nil || out.TrashedBy != nil || counted(report.ID) != size {
		t.Fatalf("moved out of .trash = %+v", out)
	}

	old := f.file(f.owner, f.ownerID, nil, "Old.pdf", "an old report")
	driveJSON[driveTestItem](t, f.call(f.owner, http.MethodPost, f.itemPath(old.ID)+"/trash", 0, nil), http.StatusOK)
	f.file(f.member, f.memberID, nil, "old.pdf", "a new report")
	back := driveJSON[driveTestItem](t, f.call(f.member, http.MethodPost, f.itemPath(old.ID)+"/restore", 0, nil), http.StatusOK)
	if back.ParentID != nil || back.Name != "Old (restored).pdf" {
		t.Fatalf("restored onto a taken name = %+v", back)
	}

	for _, change := range []map[string]any{{"name": "Bin"}, {"parent_id": docs.ID}} {
		if code := driveCode(t, f.call(f.owner, http.MethodPatch, f.itemPath(trash), 0, change), http.StatusConflict); code != "protected" {
			t.Fatalf("change %v to .trash code = %s", change, code)
		}
	}
	if code := driveCode(t, f.call(f.owner, http.MethodPost, f.itemPath(trash)+"/trash", 0, nil), http.StatusConflict); code != "protected" {
		t.Fatalf("trashing .trash code = %s", code)
	}
	if code := driveCode(t, f.call(f.owner, http.MethodPatch, f.itemPath(docs.ID), 0, map[string]any{"name": ".Trash"}), http.StatusConflict); code != "protected" {
		t.Fatalf("renaming a folder to .trash code = %s", code)
	}
}

func TestDriveRenameWithReplaceTrashesTheHolder(t *testing.T) {
	f := newDriveFixture(t, "drive-replace")
	target := f.file(f.owner, f.ownerID, nil, "a.txt", "the one in the way")
	mover := f.file(f.owner, f.ownerID, nil, "b.txt", "the new one")
	path := f.itemPath(mover.ID)

	taken := driveJSON[map[string]any](t, f.call(f.owner, http.MethodPatch, path, 0, map[string]any{"name": "A.txt"}), http.StatusConflict)
	if taken["code"] != "name_taken" || taken["item"].(map[string]any)["id"] != target.ID {
		t.Fatalf("rename onto a taken name = %v", taken)
	}
	for _, opener := range []driveTestDevice{f.member, f.owner} {
		driveJSON[map[string]any](t, f.call(opener, http.MethodPut, f.itemPath(target.ID)+"/in-use", 0, map[string]any{"editing": false}), http.StatusOK)
		if code := driveCode(t, f.call(f.owner, http.MethodPatch, path, 0, map[string]any{"name": "a.txt", "replace": true}), http.StatusLocked); code != "in_use" {
			t.Fatalf("replace while %s has it open code = %s", opener.name, code)
		}
		if res := f.call(opener, http.MethodDelete, f.itemPath(target.ID)+"/in-use", 0, nil); res.Code != http.StatusNoContent {
			t.Fatalf("clear = %d %s", res.Code, res.Body.String())
		}
	}
	before := f.changes(f.owner, 0).Head
	replaced := driveJSON[driveTestItem](t, f.call(f.owner, http.MethodPatch, path, 0, map[string]any{"name": "a.txt", "replace": true}), http.StatusOK)
	if replaced.Name != "a.txt" || f.inTrash(replaced) {
		t.Fatalf("renamed with replace = %+v", replaced)
	}
	holder := driveJSON[driveTestItem](t, f.call(f.owner, http.MethodGet, f.itemPath(target.ID), 0, nil), http.StatusOK)
	if !f.inTrash(holder) || holder.TrashedBy == nil || *holder.TrashedBy != f.ownerID || holder.Name != "a.txt" {
		t.Fatalf("the item that held the name = %+v", holder)
	}
	if after := f.changes(f.member, before); len(after.Items) != 3 || after.Items[0].Name != ".trash" || after.Items[1].ID != target.ID || after.Items[2].ID != mover.ID {
		t.Fatalf("changes after the replace = %+v", after.Items)
	}
}

func TestDrivePurgeNeedsPermissionAndTrash(t *testing.T) {
	f := newDriveFixture(t, "drive-purge")
	box := f.folder(f.owner, nil, "Old")
	inner := f.file(f.member, f.memberID, &box.ID, "old.bin", "bytes nobody else has")
	key := f.text(`SELECT blob_key FROM space_drive_items WHERE id=$1`, inner.ID)

	if code := driveCode(t, f.call(f.owner, http.MethodDelete, f.itemPath(box.ID), 0, nil), http.StatusConflict); code != "not_trashed" {
		t.Fatalf("purge outside the trash code = %s", code)
	}
	box = driveJSON[driveTestItem](t, f.call(f.member, http.MethodPost, f.itemPath(box.ID)+"/trash", box.Version, nil), http.StatusOK)
	if code := driveCode(t, f.call(f.member, http.MethodDelete, f.itemPath(box.ID), 0, nil), http.StatusForbidden); code != "forbidden" {
		t.Fatalf("purge without drive.purge code = %s", code)
	}
	before := f.changes(f.owner, 0).Head
	if got := f.call(f.owner, http.MethodDelete, f.itemPath(box.ID), 0, nil); got.Code != http.StatusNoContent {
		t.Fatalf("owner purge = %d %s", got.Code, got.Body.String())
	}
	after := f.changes(f.owner, before)
	if len(after.Items) != 2 || !after.Items[0].Deleted || !after.Items[1].Deleted || after.Head != before+2 {
		t.Fatalf("tombstones = %+v", after)
	}
	if state := f.text(`SELECT lifecycle_state FROM library_blobs WHERE r2_object_key=$1`, key); state != "deleted" {
		t.Fatalf("purged file's blob = %s, want deleted", state)
	}
	if _, err := f.store.Head(f.ctx, key); !errors.Is(err, ErrLibraryObjectNotFound) {
		t.Fatalf("purged file's object Head = %v", err)
	}
	if code := driveCode(t, f.call(f.member, http.MethodPatch, f.itemPath(inner.ID), inner.Version, map[string]any{"name": "back.bin"}), http.StatusNotFound); code != "not_found" {
		t.Fatalf("change to a purged item code = %s", code)
	}
}

func TestDriveChangesNeedResyncPastTheHorizon(t *testing.T) {
	f := newDriveFixture(t, "drive-horizon")
	early := f.folder(f.owner, nil, "Early")
	gone := f.folder(f.owner, nil, "Gone")
	gone = driveJSON[driveTestItem](t, f.call(f.owner, http.MethodPost, f.itemPath(gone.ID)+"/trash", gone.Version, nil), http.StatusOK)
	if got := f.call(f.owner, http.MethodDelete, f.itemPath(gone.ID), 0, nil); got.Code != http.StatusNoContent {
		t.Fatalf("purge = %d %s", got.Code, got.Body.String())
	}
	f.exec(`UPDATE space_drive_items SET deleted_at=NOW()-interval '91 days' WHERE id=$1`, gone.ID)
	if _, err := f.drive.PruneTombstones(f.ctx, 1000); err != nil {
		t.Fatal(err)
	}
	horizon, _ := strconv.ParseInt(f.text(`SELECT drive_tombstone_horizon::text FROM space_drives WHERE space_id=$1`, f.spaceID), 10, 64)
	if horizon <= early.Seq {
		t.Fatalf("horizon = %d, want past %d", horizon, early.Seq)
	}
	if code := driveCode(t, f.call(f.member, http.MethodGet, f.base+"/changes?since="+strconv.FormatInt(early.Seq, 10), 0, nil), http.StatusGone); code != "resync_required" {
		t.Fatalf("stale cursor code = %s", code)
	}
	full := f.changes(f.member, 0)
	if len(full.Items) != 2 || full.Items[0].ID != early.ID || full.Items[1].Name != ".trash" {
		t.Fatalf("full listing = %+v", full)
	}
	if fresh := f.changes(f.member, horizon); len(fresh.Items) != 0 {
		t.Fatalf("cursor at the horizon = %+v", fresh)
	}
}

func TestDriveStartsWithAProtectedMistyignore(t *testing.T) {
	f := newDriveFixture(t, "drive-starter-ignore")
	f.drive.StarterIgnore = true
	all := f.changes(f.member, 0)
	var starter *driveTestItem
	for index := range all.Items {
		if all.Items[index].Name == ".mistyignore" && all.Items[index].ParentID == nil {
			starter = &all.Items[index]
		}
	}
	if starter == nil {
		t.Fatalf("changes since 0 = %+v, want the starter .mistyignore", all)
	}
	if again := f.changes(f.member, 0); len(again.Items) != len(all.Items) {
		t.Fatalf("a second read made another file: %+v", again)
	}
	if code := driveCode(t, f.call(f.member, http.MethodPost, f.itemPath(starter.ID)+"/trash", 0, nil), http.StatusConflict); code != "protected" {
		t.Fatalf("trashing the root .mistyignore code = %s", code)
	}
}

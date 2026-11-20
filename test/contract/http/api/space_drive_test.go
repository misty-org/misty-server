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

// Items come in, the change feed lists them in one order, and the feed's
// cursor picks up exactly where it stopped.
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
	// The same content again needs no upload.
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
	// Names are unique in a folder ignoring case, and the answer names who has it.
	taken := driveJSON[map[string]any](t, f.call(f.owner, http.MethodPost, f.base+"/items", 0,
		map[string]any{"parent_id": docs.ID, "name": "report.PDF", "kind": "folder"}), http.StatusConflict)
	if taken["code"] != "name_taken" || taken["item"].(map[string]any)["id"] != report.ID {
		t.Fatalf("case-insensitive clash = %v", taken)
	}
}

// A create that names its own id is safe to send again after a lost answer,
// and an id that is someone else's is never taken over.
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
	// Another person's Space, made in this fixture: a second fixture would wait
	// forever on the test database lock this one holds.
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

// The last write wins: a change without If-Match applies whatever the
// version, and only a sender that names one (a merged save) is held to it.
func TestDrivePatchIfMatchIsOptional(t *testing.T) {
	f := newDriveFixture(t, "drive-patch")
	docs := f.folder(f.owner, nil, "Docs")
	notes := f.file(f.owner, f.ownerID, nil, "notes.txt", "first")
	path := f.itemPath(notes.ID)

	renamed := driveJSON[driveTestItem](t, f.call(f.owner, http.MethodPatch, path, 0, map[string]any{"name": "renamed.txt"}), http.StatusOK)
	if renamed.Version != notes.Version+1 || renamed.Name != "renamed.txt" || renamed.Revived || renamed.Replaced != nil {
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
	// The same change without If-Match goes through.
	mine := driveJSON[driveTestItem](t, f.call(f.member, http.MethodPatch, path, 0, map[string]any{"name": "mine.txt"}), http.StatusOK)
	if mine.Name != "mine.txt" || mine.ModifiedBy != f.memberID {
		t.Fatalf("unconditional change from the member = %+v", mine)
	}
	// New content, from a fresh upload, against the current version.
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

// A save over content its sender never saw replaces it, and the replaced
// content is kept as a trashed copy in the same folder: the same blob, who
// last changed it, and replaced_of naming the item. Only a change of content
// counts as unseen; the copy's bytes stay counted until the trash empties.
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

	// Ben's save from the current version replaces nothing he didn't see.
	ben := save(f.member, f.memberID, "ben's draft", notes.Version)
	if ben.Replaced != nil {
		t.Fatalf("save from the current version kept a copy: %+v", ben.Replaced)
	}
	benKey := f.text(`SELECT blob_key FROM space_drive_items WHERE id=$1`, notes.ID)
	// A rename since doesn't count as content Ana missed.
	renamed := driveJSON[driveTestItem](t, f.call(f.member, http.MethodPatch, path, 0, map[string]any{"name": "Notes.txt"}), http.StatusOK)
	before := f.changes(f.owner, 0).Head

	// Ana's save from version 1 never saw Ben's draft: it wins, and his draft is kept.
	ana := save(f.owner, f.ownerID, "ana's draft", notes.Version)
	copy := ana.Replaced
	if copy == nil {
		t.Fatalf("stale save kept no copy: %+v", ana)
	}
	if ana.Name != "Notes.txt" || ana.ModifiedBy != f.ownerID || ana.Version != renamed.Version+1 {
		t.Fatalf("item after the stale save = %+v", ana)
	}
	if !regexp.MustCompile(`^Notes \(replaced \d{4}-\d{2}-\d{2} \d{4}\)\.txt$`).MatchString(copy.Name) {
		t.Fatalf("copy name = %q", copy.Name)
	}
	if !copy.Trashed || copy.TrashHold || copy.TrashedBy == nil || *copy.TrashedBy != f.ownerID || copy.ReplacedOf == nil || *copy.ReplacedOf != notes.ID ||
		copy.Kind != "file" || copy.ParentID == nil || *copy.ParentID != docs.ID || copy.ModifiedBy != f.memberID ||
		copy.SHA256 == nil || *copy.SHA256 != *ben.SHA256 || copy.Size != ben.Size {
		t.Fatalf("replaced copy = %+v", copy)
	}
	if key := f.text(`SELECT blob_key FROM space_drive_items WHERE id=$1`, copy.ID); key != benKey {
		t.Fatalf("copy's blob = %s, want the replaced one %s", key, benKey)
	}
	// The change feed carries the copy and the item, in that order.
	after := f.changes(f.member, before)
	if len(after.Items) != 2 || after.Items[0].ID != copy.ID || after.Items[1].ID != notes.ID || after.Items[0].ReplacedOf == nil {
		t.Fatalf("changes after the stale save = %+v", after.Items)
	}
	// Ben's bytes stay counted, on the copy; Ana's count on the item.
	if got := counted(copy.ID); got != strconv.FormatInt(ben.Size, 10) {
		t.Fatalf("copy counts %s bytes, want %d", got, ben.Size)
	}
	if got := counted(notes.ID); got != strconv.FormatInt(ana.Size, 10) {
		t.Fatalf("item counts %s bytes, want %d", got, ana.Size)
	}
	trash := driveJSON[struct {
		Items []driveTestItem `json:"items"`
	}](t, f.call(f.member, http.MethodGet, f.base+"/trash", 0, nil), http.StatusOK)
	if len(trash.Items) != 1 || trash.Items[0].ID != copy.ID {
		t.Fatalf("trash = %+v", trash.Items)
	}

	// A save with no base at all keeps what it replaces too.
	blind := save(f.member, f.memberID, "blind save", 0)
	if blind.Replaced == nil || blind.Replaced.SHA256 == nil || *blind.Replaced.SHA256 != *ana.SHA256 || blind.Replaced.ID == copy.ID {
		t.Fatalf("save without a base = %+v", blind)
	}

	// The trash's 30 days end the copy like anything trashed, and its bytes go.
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

// A delete needs no If-Match, and a write after it wins: a change to a
// trashed item, or to one in a trashed folder, brings it back, and so does a
// new item made in a trashed folder. The trash keeps an item's bytes, and
// restore brings it back under a free name.
func TestDriveWritesReviveTrashedItems(t *testing.T) {
	f := newDriveFixture(t, "drive-trash")
	report := f.file(f.owner, f.ownerID, nil, "Report.pdf", "the report")
	key := f.text(`SELECT blob_key FROM space_drive_items WHERE id=$1`, report.ID)

	if code := driveCode(t, f.call(f.owner, http.MethodPost, f.itemPath(report.ID)+"/trash", report.Version+5, nil), http.StatusConflict); code != "version_conflict" {
		t.Fatalf("trash with a stale If-Match code = %s", code)
	}
	trashed := driveJSON[driveTestItem](t, f.call(f.owner, http.MethodPost, f.itemPath(report.ID)+"/trash", 0, map[string]any{"held": false}), http.StatusOK)
	if !trashed.Trashed || trashed.Version != report.Version+1 {
		t.Fatalf("trashed = %+v", trashed)
	}
	if code := driveCode(t, f.call(f.owner, http.MethodPost, f.itemPath(report.ID)+"/trash", 0, nil), http.StatusConflict); code != "trashed" {
		t.Fatalf("trash of a trashed item code = %s", code)
	}
	// The trash never removes bytes.
	if state := f.text(`SELECT lifecycle_state FROM library_blobs WHERE r2_object_key=$1`, key); state != "ready" {
		t.Fatalf("trashed file's blob = %s, want ready", state)
	}
	if _, err := f.store.Head(f.ctx, key); err != nil {
		t.Fatalf("trashed file's object: %v", err)
	}
	// The member's edit after the delete wins: the item is back, edited.
	revived := driveJSON[driveTestItem](t, f.call(f.member, http.MethodPatch, f.itemPath(report.ID), 0, map[string]any{"name": "Report (edited).pdf"}), http.StatusOK)
	if revived.Trashed || !revived.Revived || revived.Name != "Report (edited).pdf" || revived.ModifiedBy != f.memberID || revived.Version <= trashed.Version {
		t.Fatalf("edit of a trashed item = %+v", revived)
	}
	if again := driveJSON[driveTestItem](t, f.call(f.member, http.MethodPatch, f.itemPath(report.ID), 0, map[string]any{"mtime_ms": 5}), http.StatusOK); again.Revived {
		t.Fatalf("edit of a live item said revived: %+v", again)
	}

	// A folder's contents hide with it; a change inside brings the folder back.
	box := f.folder(f.owner, nil, "Box")
	inner := f.file(f.owner, f.ownerID, &box.ID, "inner.txt", "inside the box")
	driveJSON[driveTestItem](t, f.call(f.owner, http.MethodPost, f.itemPath(box.ID)+"/trash", 0, nil), http.StatusOK)
	movedInside := driveJSON[driveTestItem](t, f.call(f.member, http.MethodPatch, f.itemPath(inner.ID), 0, map[string]any{"name": "moved.txt"}), http.StatusOK)
	if !movedInside.Revived || movedInside.Name != "moved.txt" {
		t.Fatalf("edit inside a trashed folder = %+v", movedInside)
	}
	if folder := driveJSON[driveTestItem](t, f.call(f.owner, http.MethodGet, f.itemPath(box.ID), 0, nil), http.StatusOK); folder.Trashed {
		t.Fatalf("the folder stayed in the trash: %+v", folder)
	}
	// A new item made in a trashed folder brings the folder back.
	driveJSON[driveTestItem](t, f.call(f.owner, http.MethodPost, f.itemPath(box.ID)+"/trash", 0, nil), http.StatusOK)
	made := driveJSON[driveTestItem](t, f.call(f.member, http.MethodPost, f.base+"/items", 0, map[string]any{"parent_id": box.ID, "name": "new", "kind": "folder"}), http.StatusCreated)
	if !made.Revived || made.ParentID == nil || *made.ParentID != box.ID {
		t.Fatalf("create in a trashed folder = %+v", made)
	}
	if folder := driveJSON[driveTestItem](t, f.call(f.owner, http.MethodGet, f.itemPath(box.ID), 0, nil), http.StatusOK); folder.Trashed {
		t.Fatalf("create left the folder in the trash: %+v", folder)
	}
	if plain := driveJSON[driveTestItem](t, f.call(f.member, http.MethodPost, f.base+"/items", 0, map[string]any{"parent_id": box.ID, "name": "plain", "kind": "folder"}), http.StatusCreated); plain.Revived {
		t.Fatalf("create in a live folder said revived: %+v", plain)
	}

	// Someone made another Report.pdf while the old one was in the trash; it comes back renamed.
	old := f.file(f.owner, f.ownerID, nil, "Old.pdf", "an old report")
	driveJSON[driveTestItem](t, f.call(f.owner, http.MethodPost, f.itemPath(old.ID)+"/trash", 0, nil), http.StatusOK)
	f.file(f.member, f.memberID, nil, "old.pdf", "a new report")
	restored := driveJSON[driveTestItem](t, f.call(f.member, http.MethodPost, f.itemPath(old.ID)+"/restore", 0, nil), http.StatusOK)
	if restored.Trashed || restored.Name != "Old (restored).pdf" {
		t.Fatalf("restored = %+v", restored)
	}
}

// A rename or move onto a taken name is refused unless it asks to replace:
// then the item holding the name goes to the trash first, unless anyone has it
// open, the asking computer included.
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
	if replaced.Name != "a.txt" || replaced.Trashed {
		t.Fatalf("renamed with replace = %+v", replaced)
	}
	holder := driveJSON[driveTestItem](t, f.call(f.owner, http.MethodGet, f.itemPath(target.ID), 0, nil), http.StatusOK)
	if !holder.Trashed || holder.TrashedBy == nil || *holder.TrashedBy != f.ownerID || holder.Name != "a.txt" {
		t.Fatalf("the item that held the name = %+v", holder)
	}
	if after := f.changes(f.member, before); len(after.Items) != 2 || after.Items[0].ID != target.ID || after.Items[1].ID != mover.ID {
		t.Fatalf("changes after the replace = %+v", after.Items)
	}
}

// Deleting for good needs drive.purge and an item already in the trash; it
// leaves tombstones and retires bytes nothing else uses.
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

// A cursor older than the pruned tombstones can't know what was purged, so it
// starts over; since 0 and newer cursors read on.
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
	if len(full.Items) != 1 || full.Items[0].ID != early.ID {
		t.Fatalf("full listing = %+v", full)
	}
	if fresh := f.changes(f.member, horizon); len(fresh.Items) != 0 {
		t.Fatalf("cursor at the horizon = %+v", fresh)
	}
}

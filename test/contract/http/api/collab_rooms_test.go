package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/misty-org/misty-server/internal/platform/db"
	. "github.com/misty-org/misty-server/internal/platform/httpapi"
	"github.com/misty-org/misty-server/internal/platform/security"
)

func signedCollabConfig(t *testing.T) (JournalCollabConfig, []byte) {
	t.Helper()
	testCollabConfig(t)
	secret := bytes.Repeat([]byte{7}, 32)
	t.Setenv("JOURNAL_COLLAB_PROJECTION_SECRET", base64.StdEncoding.EncodeToString(secret))
	config, err := JournalCollabConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	return config, secret
}

func signedCallback(t *testing.T, secret []byte, path string, payload any) *http.Request {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	request.RemoteAddr = "203.0.113.40:4000"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Misty-Timestamp", timestamp)
	request.Header.Set("X-Misty-Signature", SignServicePayload(secret, timestamp, body))
	return request
}

func TestSignedJournalCallbacksSkipTheRateLimits(t *testing.T) {
	config, secret := signedCollabConfig(t)
	guard := NewAbuseGuard(AbusePolicy{
		TotalLimit: 5, TotalWindow: time.Minute,
		StrikesBeforeBlock: 3, StrikeWindow: time.Minute,
		BaseBlock: time.Minute, MaxBlock: time.Minute,
	})
	limiter := NewAPIRateLimiter().WithAbuseGuard(guard)
	var seen []byte
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	})
	handler := TrustSignedServiceCallbacks(func() JournalCollabConfig { return config })(
		guard.Middleware(limiter.Middleware(final)))
	payload := map[string]any{"note_id": "note_abcdefabcdefabcdef", "revision": 1}
	want, _ := json.Marshal(payload)
	for _, path := range []string{"/v1/internal/journal/note-projections", "/v1/internal/journal/file-texts"} {
		for i := 0; i < 60; i++ {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, signedCallback(t, secret, path, payload))
			if recorder.Code != http.StatusOK {
				t.Fatalf("signed callback %d to %s = %d, want it past the limits", i, path, recorder.Code)
			}
			if !bytes.Equal(seen, want) {
				t.Fatalf("handler read %q, want the signed body %q", seen, want)
			}
		}
	}

	allowed := 0
	for i := 0; i < 60; i++ {
		request := signedCallback(t, []byte("not the secret, not the secret!!"), "/v1/internal/journal/file-texts", payload)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code == http.StatusOK {
			allowed++
		}
	}
	if allowed > 30 {
		t.Fatalf("unsigned callbacks passed %d times, want the public limits to hold", allowed)
	}
}

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

func TestCollaborationTicketsAreLimitedPerAccount(t *testing.T) {
	limiter := NewAPIRateLimiter()
	handler := hardeningHandler(limiter)
	path := "/spaces/space_abcdefabcdefabcdef/apps/sketch/rooms/board/collaboration-ticket"
	signer, err := security.SessionSignerFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	signed := func(userID string) string {
		token, err := signer.Mint(userID, "sid-"+userID, "access", time.Now().Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	send := func(token, address string) int {
		request := httptest.NewRequest(http.MethodPost, path, nil)
		request.RemoteAddr = address
		request.Header.Set("Authorization", "Bearer "+token)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder.Code
	}
	first, second := signed("user_first_person"), signed("user_second_person")
	for i := 0; i < 120; i++ {
		if code := send(first, "203.0.113.41:1234"); code != http.StatusOK {
			t.Fatalf("ticket %d for one person = %d, want the per-account budget", i, code)
		}
	}
	if code := send(first, "203.0.113.41:1234"); code != http.StatusTooManyRequests {
		t.Fatalf("ticket past the budget = %d, want 429", code)
	}
	if code := send(second, "203.0.113.41:1234"); code != http.StatusOK {
		t.Fatalf("another person behind the same address = %d, want their own budget", code)
	}

	allowed := 0
	for i := 0; i < 200; i++ {
		if send("made-up-token-"+strconv.Itoa(i), "203.0.113.42:1234") == http.StatusOK {
			allowed++
		}
	}
	if allowed > 120 {
		t.Fatalf("unsigned tokens passed %d times, want them held to one address's budget", allowed)
	}
}

func TestSpaceAppRoomAccessAndTickets(t *testing.T) {
	f := newDriveFixture(t, "app-rooms")
	config := testCollabConfig(t)
	if _, err := f.database.InstallSpaceApp(f.ctx, f.ownerID, f.spaceID, "sketch", "1.0.0", []string{"files"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.SpaceAppRoomAccess(f.ctx, f.memberID, f.spaceID, "sketch", "board"); !errors.Is(err, db.ErrSpaceForbidden) {
		t.Fatalf("room access without the rooms permission = %v, want forbidden", err)
	}
	if _, err := f.database.InstallSpaceApp(f.ctx, f.ownerID, f.spaceID, "sketch", "1.0.0", []string{"rooms"}, nil); err != nil {
		t.Fatal(err)
	}
	for _, room := range []string{".", "..", "...", "", "a/b"} {
		if _, err := f.database.SpaceAppRoomAccess(f.ctx, f.memberID, f.spaceID, "sketch", room); !errors.Is(err, db.ErrSpaceNotFound) {
			t.Fatalf("room id %q = %v, want it refused", room, err)
		}
	}
	grant, err := f.database.SpaceAppRoomAccess(f.ctx, f.memberID, f.spaceID, "sketch", "board.v1")
	if err != nil {
		t.Fatal(err)
	}
	if grant.Role != "editor" || len(grant.Generation) != 16 {
		t.Fatalf("grant = %+v, want an editor with a fresh room generation", grant)
	}
	ticket, err := config.MintAppRoomTicket(f.memberID, f.spaceID, "sketch", "board.v1", grant.Generation, grant.Role, false)
	if err != nil {
		t.Fatal(err)
	}
	claims := decodeTicketClaims(t, ticket.Ticket)
	if claims.Role != "editor" || claims.ResourceID != AppRoomResourceID(f.spaceID, "sketch", "board.v1") || claims.Room != ticket.Room {
		t.Fatalf("app room claims = %+v", claims)
	}
	viewer, err := config.MintAppRoomTicket(f.memberID, f.spaceID, "sketch", "board.v1", grant.Generation, "viewer", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeTicketClaims(t, viewer.Ticket).Role; got != "viewer" || viewer.Role != "viewer" {
		t.Fatalf("viewer ticket role = %q / %q, want viewer", got, viewer.Role)
	}
	if err := f.database.RecordCollabRoom(f.ctx, db.CollabRoomRecord{
		Party: db.CollabPartyApp, Room: ticket.Room, SpaceID: f.spaceID,
		ResourceID: AppRoomResourceID(f.spaceID, "sketch", "board.v1"), AppID: "sketch", UserID: f.memberID,
		Permissions: []string{"rooms"},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := f.database.InstallSpaceApp(f.ctx, f.ownerID, f.spaceID, "sketch", "1.1.0", []string{"files"}, nil); err != nil {
		t.Fatal(err)
	}
	if got := f.text(`SELECT count(*)::text FROM space_collab_room_control_outbox
		WHERE space_id=$1 AND room=$2 AND command='disconnect'`, f.spaceID, ticket.Room); got != "1" {
		t.Fatalf("disconnects after losing rooms = %s, want 1", got)
	}

	if err := f.database.RemoveSpaceApp(f.ctx, f.ownerID, f.spaceID, "sketch"); err != nil {
		t.Fatal(err)
	}
	if got := f.text(`SELECT count(*)::text FROM space_collab_room_control_outbox
		WHERE space_id=$1 AND room=$2 AND command='purge'`, f.spaceID, ticket.Room); got != "1" {
		t.Fatalf("purges after removal = %s, want 1", got)
	}

	if _, err := f.database.InstallSpaceApp(f.ctx, f.ownerID, f.spaceID, "sketch", "2.0.0", []string{"rooms"}, nil); err != nil {
		t.Fatal(err)
	}
	again, err := f.database.SpaceAppRoomAccess(f.ctx, f.memberID, f.spaceID, "sketch", "board.v1")
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := config.MintAppRoomTicket(f.memberID, f.spaceID, "sketch", "board.v1", again.Generation, again.Role, false)
	if err != nil {
		t.Fatal(err)
	}
	if again.Generation == grant.Generation || fresh.Room == ticket.Room {
		t.Fatal("a reinstalled app opened the purged room again")
	}
	if err := f.database.RecordCollabRoom(f.ctx, db.CollabRoomRecord{
		Party: db.CollabPartyApp, Room: fresh.Room, SpaceID: f.spaceID,
		ResourceID: AppRoomResourceID(f.spaceID, "sketch", "board.v1"), AppID: "sketch", UserID: f.memberID,
		Permissions: []string{"rooms"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.database.LeaveSpace(f.ctx, f.memberID, f.spaceID); err != nil {
		t.Fatal(err)
	}
	if got := f.text(`SELECT payload->'user_ids'->>0 FROM space_collab_room_control_outbox
		WHERE space_id=$1 AND room=$2 AND command='disconnect'`, f.spaceID, fresh.Room); got != f.memberID {
		t.Fatalf("disconnect after leaving names %q, want the member", got)
	}
	if err := f.database.RecordCollabRoom(f.ctx, db.CollabRoomRecord{
		Party: db.CollabPartyApp, Room: fresh.Room, SpaceID: f.spaceID,
		ResourceID: AppRoomResourceID(f.spaceID, "sketch", "board.v1"), AppID: "sketch", UserID: f.memberID,
		Permissions: []string{"rooms"},
	}); !errors.Is(err, db.ErrSpaceForbidden) {
		t.Fatalf("recording a room for someone who left = %v, want forbidden", err)
	}
}

func TestRemovingAnAppDisconnectsItsFileRoomsWithoutPurgingThem(t *testing.T) {
	f := newDriveFixture(t, "app-removal-file-rooms")
	item := f.file(f.owner, f.ownerID, nil, "board.sketch", "{}")
	if _, err := f.database.InstallSpaceApp(f.ctx, f.ownerID, f.spaceID, "sketch", "1.0.0", []string{"files", "rooms"}, nil); err != nil {
		t.Fatal(err)
	}
	fileRoom := "file-room-of-the-sketch"
	if err := f.database.RecordCollabRoom(f.ctx, db.CollabRoomRecord{
		Party: db.CollabPartyFile, Room: fileRoom, SpaceID: f.spaceID,
		ResourceID: FileRoomResourceID(f.spaceID, item.ID, "sketch", "app"),
		AppID:      "sketch", ItemID: item.ID, UserID: f.ownerID,
		Permissions: []string{"rooms", "files"},
	}); err != nil {
		t.Fatal(err)
	}
	appRoom := "app-room-of-the-sketch"
	if err := f.database.RecordCollabRoom(f.ctx, db.CollabRoomRecord{
		Party: db.CollabPartyApp, Room: appRoom, SpaceID: f.spaceID,
		ResourceID: AppRoomResourceID(f.spaceID, "sketch", "board"), AppID: "sketch", UserID: f.ownerID,
		Permissions: []string{"rooms"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.database.RemoveSpaceApp(f.ctx, f.ownerID, f.spaceID, "sketch"); err != nil {
		t.Fatal(err)
	}
	if got := f.text(`SELECT count(*)::text FROM space_collab_room_control_outbox
		WHERE space_id=$1 AND room=$2 AND command='purge'`, f.spaceID, fileRoom); got != "0" {
		t.Fatalf("purges of the file room after removal = %s, want none", got)
	}
	if got := f.text(`SELECT count(*)::text FROM space_collab_room_control_outbox
		WHERE space_id=$1 AND room=$2 AND command='disconnect'`, f.spaceID, fileRoom); got == "0" {
		t.Fatal("the file room wasn't disconnected after removal")
	}
	if got := f.text(`SELECT count(*)::text FROM space_collab_rooms WHERE party=$1 AND room=$2`,
		db.CollabPartyFile, fileRoom); got != "1" {
		t.Fatalf("file room records after removal = %s, want it kept for the Space's purge", got)
	}
	if got := f.text(`SELECT count(*)::text FROM space_collab_room_control_outbox
		WHERE space_id=$1 AND room=$2 AND command='purge'`, f.spaceID, appRoom); got != "1" {
		t.Fatalf("purges of the app room after removal = %s, want 1", got)
	}
}

func TestRemovingAnAppKeepsSharedTextRoomsOpenThroughOtherApps(t *testing.T) {
	f := newDriveFixture(t, "app-removal-text-rooms")
	shared := f.file(f.owner, f.ownerID, nil, "shared.md", "hello")
	alone := f.file(f.owner, f.ownerID, nil, "alone.md", "hello")
	for _, appID := range []string{"sketch", "writer"} {
		if _, err := f.database.InstallSpaceApp(f.ctx, f.ownerID, f.spaceID, appID, "1.0.0", []string{"files", "rooms"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	record := func(room, itemID, appID string) {
		t.Helper()
		if err := f.database.RecordCollabRoom(f.ctx, db.CollabRoomRecord{
			Party: db.CollabPartyFile, Room: room, SpaceID: f.spaceID,
			ResourceID: FileRoomResourceID(f.spaceID, itemID, appID, "text"),
			AppID:      appID, ItemID: itemID, UserID: f.ownerID,
			Permissions: []string{"rooms", "files"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	record("text-room-shared", shared.ID, "sketch")
	record("text-room-shared", shared.ID, "writer")
	record("text-room-alone", alone.ID, "sketch")
	if err := f.database.RemoveSpaceApp(f.ctx, f.ownerID, f.spaceID, "sketch"); err != nil {
		t.Fatal(err)
	}
	if got := f.text(`SELECT count(*)::text FROM space_collab_room_control_outbox
		WHERE space_id=$1 AND room=$2 AND command='disconnect'`, f.spaceID, "text-room-shared"); got != "0" {
		t.Fatalf("disconnects of a text room still open through another app = %s, want none", got)
	}
	if got := f.text(`SELECT count(*)::text FROM space_collab_room_control_outbox
		WHERE space_id=$1 AND room=$2 AND command='disconnect'`, f.spaceID, "text-room-alone"); got == "0" {
		t.Fatal("a text room reached only through the removed app wasn't disconnected")
	}
}

func TestDriveFileRoomAccessNeedsTheFilesPermission(t *testing.T) {
	f := newDriveFixture(t, "file-room-files")
	item := f.file(f.owner, f.ownerID, nil, "notes.md", "hello")
	if _, err := f.database.InstallSpaceApp(f.ctx, f.ownerID, f.spaceID, "writer", "1.0.0", []string{"rooms"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.DriveFileRoomAccess(f.ctx, f.ownerID, f.spaceID, item.ID, "writer"); !errors.Is(err, db.ErrSpaceForbidden) {
		t.Fatalf("file room with rooms alone = %v, want forbidden", err)
	}
	if _, err := f.database.InstallSpaceApp(f.ctx, f.ownerID, f.spaceID, "writer", "1.0.0", []string{"files", "rooms"}, nil); err != nil {
		t.Fatal(err)
	}
	access, err := f.database.DriveFileRoomAccess(f.ctx, f.ownerID, f.spaceID, item.ID, "writer")
	if err != nil {
		t.Fatal(err)
	}
	if !access.CanWrite || access.Item.ID != item.ID || len(access.RoomGeneration) != 16 {
		t.Fatalf("file room access = %+v", access)
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

func TestFileRoomTextSaveWithAStaleBaseIsRefused(t *testing.T) {
	f := newDriveFixture(t, "file-room-stale")
	config, secret := signedCollabConfig(t)
	f.drive.SetJournalCollab(config)
	f.router.Post("/internal/journal/file-texts", f.drive.DriveFileRoomText())
	item := f.file(f.owner, f.ownerID, nil, "plan.md", "first")
	loaded := item.Version

	uploadID, sha := f.upload(f.owner, f.ownerID, []byte("outside edit"))
	driveJSON[driveTestItem](t, f.call(f.owner, http.MethodPatch, f.itemPath(item.ID), item.Version, map[string]any{
		"sha256": sha, "size": len("outside edit"), "upload_id": uploadID,
	}), http.StatusOK)
	outside := f.text(`SELECT version::text FROM space_drive_items WHERE id=$1`, item.ID)

	save := func(text string, base int64, want int) map[string]any {
		t.Helper()
		recorder := httptest.NewRecorder()
		f.router.ServeHTTP(recorder, signedCallback(t, secret, "/internal/journal/file-texts", map[string]any{
			"space_id": f.spaceID, "item_id": item.ID, "user_id": f.ownerID, "text": text, "base_version": base,
		}))
		return driveJSON[map[string]any](t, recorder, want)
	}
	version := func() string {
		return f.text(`SELECT version::text FROM space_drive_items WHERE id=$1`, item.ID)
	}
	replaced := func() string {
		return f.text(`SELECT count(*)::text FROM space_drive_items WHERE replaced_of=$1`, item.ID)
	}

	refused := save("from the room", loaded, http.StatusConflict)
	if got := strconv.FormatInt(int64(refused["version"].(float64)), 10); got != outside {
		t.Fatalf("refused save names version %s, want the drive's %s", got, outside)
	}
	if version() != outside || replaced() != "0" {
		t.Fatalf("a stale save wrote: version %s (want %s), replaced copies %s", version(), outside, replaced())
	}
	save("from the room again", 0, http.StatusConflict)
	if version() != outside || replaced() != "0" {
		t.Fatalf("a save with no base wrote: version %s, replaced copies %s", version(), replaced())
	}
	current, _ := strconv.ParseInt(outside, 10, 64)
	savedFrom := time.Now().UnixMilli()
	answer := save("a current save", current, http.StatusOK)
	if int64(answer["version"].(float64)) <= current || replaced() != "0" {
		t.Fatalf("current save = %v, replaced copies %s", answer, replaced())
	}
	if got := f.text(`SELECT (mtime_ms >= $2)::text FROM space_drive_items WHERE id=$1`, item.ID, savedFrom); got != "true" {
		t.Fatal("a room's text save kept the file's old modified time, want the time of the save")
	}
	same := save("a current save", loaded, http.StatusOK)
	if same["version"] != answer["version"] {
		t.Fatalf("saving the file's own text from a stale base = %v, want it taken as is", same)
	}
}

func TestFileRoomTextSaveAfterARenameIsTakenOverTheRenamedFile(t *testing.T) {
	f := newDriveFixture(t, "file-room-renamed")
	config, secret := signedCollabConfig(t)
	f.drive.SetJournalCollab(config)
	f.router.Post("/internal/journal/file-texts", f.drive.DriveFileRoomText())
	item := f.file(f.owner, f.ownerID, nil, "plan.md", "first")
	loaded := item.Version
	driveJSON[driveTestItem](t, f.call(f.owner, http.MethodPatch, f.itemPath(item.ID), item.Version, map[string]any{
		"name": "plan-renamed.md",
	}), http.StatusOK)

	recorder := httptest.NewRecorder()
	f.router.ServeHTTP(recorder, signedCallback(t, secret, "/internal/journal/file-texts", map[string]any{
		"space_id": f.spaceID, "item_id": item.ID, "user_id": f.ownerID, "text": "from the room", "base_version": loaded,
	}))
	answer := driveJSON[map[string]any](t, recorder, http.StatusOK)
	if int64(answer["version"].(float64)) <= loaded+1 {
		t.Fatalf("save over a renamed file = %v, want a version after the rename", answer)
	}
	if got := f.text(`SELECT name||'|'||byte_size::text FROM space_drive_items WHERE id=$1`, item.ID); got != "plan-renamed.md|13" {
		t.Fatalf("renamed file after the save = %s", got)
	}
	if got := f.text(`SELECT count(*)::text FROM space_drive_items WHERE replaced_of=$1`, item.ID); got != "0" {
		t.Fatalf("a save over a rename kept %s replaced copies, want none", got)
	}
	saved := int64(answer["version"].(float64))
	driveJSON[driveTestItem](t, f.call(f.owner, http.MethodPatch, f.itemPath(item.ID), saved, map[string]any{
		"name": "plan-final.md",
	}), http.StatusOK)
	save := func(base int64, want int) map[string]any {
		t.Helper()
		again := httptest.NewRecorder()
		f.router.ServeHTTP(again, signedCallback(t, secret, "/internal/journal/file-texts", map[string]any{
			"space_id": f.spaceID, "item_id": item.ID, "user_id": f.ownerID, "text": "after the second rename", "base_version": base,
		}))
		return driveJSON[map[string]any](t, again, want)
	}
	save(saved+2, http.StatusConflict)
	if got := save(saved+1, http.StatusOK); int64(got["version"].(float64)) <= saved+1 {
		t.Fatalf("save from the version a rename gave = %v, want it taken over the renamed file", got)
	}
}

func TestFileRoomTextConflictCopyIsWrittenBesideTheFile(t *testing.T) {
	f := newDriveFixture(t, "file-room-copy")
	config, secret := signedCollabConfig(t)
	f.drive.SetJournalCollab(config)
	f.router.Post("/internal/journal/file-texts", f.drive.DriveFileRoomText())
	item := f.file(f.owner, f.ownerID, nil, "plan.md", "first")
	before := f.text(`SELECT version::text FROM space_drive_items WHERE id=$1`, item.ID)

	recorder := httptest.NewRecorder()
	f.router.ServeHTTP(recorder, signedCallback(t, secret, "/internal/journal/file-texts", map[string]any{
		"space_id": f.spaceID, "item_id": item.ID, "user_id": f.ownerID, "text": "kept from the room",
		"base_version": 1, "conflict_copy": true,
	}))
	answer := driveJSON[map[string]any](t, recorder, http.StatusOK)
	copyID, _ := answer["item_id"].(string)
	name, _ := answer["name"].(string)
	if copyID == "" || copyID == item.ID {
		t.Fatalf("conflict copy answer = %v, want a new item", answer)
	}
	if !strings.HasPrefix(name, "plan (conflicted copy ") || !strings.HasSuffix(name, ".md") {
		t.Fatalf("conflict copy name = %q", name)
	}
	if got := f.text(`SELECT version::text FROM space_drive_items WHERE id=$1`, item.ID); got != before {
		t.Fatalf("the original changed to version %s, want %s", got, before)
	}
	if got := f.text(`SELECT COALESCE(parent_id,'')||'|'||byte_size::text||'|'||(trashed_at IS NULL)::text FROM space_drive_items WHERE id=$1`, copyID); got != "|18|true" {
		t.Fatalf("conflict copy row = %s, want beside the file with the room's text", got)
	}

	again := httptest.NewRecorder()
	f.router.ServeHTTP(again, signedCallback(t, secret, "/internal/journal/file-texts", map[string]any{
		"space_id": f.spaceID, "item_id": item.ID, "user_id": f.ownerID, "text": "kept again",
		"base_version": 1, "conflict_copy": true,
	}))
	second := driveJSON[map[string]any](t, again, http.StatusOK)
	if second["name"] == name {
		t.Fatalf("a second conflict copy took the same name %q", name)
	}
}

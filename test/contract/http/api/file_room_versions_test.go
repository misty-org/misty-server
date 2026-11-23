package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestFileRoomVersionsAnswerTheDrivesOwnVersionsToSignedCallsOnly(t *testing.T) {
	f := newDriveFixture(t, "file-room-versions")
	config, secret := signedCollabConfig(t)
	f.drive.SetJournalCollab(config)
	f.router.Post("/internal/journal/file-versions", f.drive.DriveFileRoomVersions())
	item := f.file(f.owner, f.ownerID, nil, "board.bin", "first")
	path := f.itemPath(item.ID)

	ask := func(userID, itemID string, want int) map[string]any {
		t.Helper()
		recorder := httptest.NewRecorder()
		f.router.ServeHTTP(recorder, signedCallback(t, secret, "/internal/journal/file-versions", map[string]any{
			"space_id": f.spaceID, "item_id": itemID, "user_id": userID,
		}))
		return driveJSON[map[string]any](t, recorder, want)
	}
	versions := func(answer map[string]any) (int64, int64) {
		t.Helper()
		version, ok := answer["version"].(float64)
		content, okContent := answer["content_version"].(float64)
		if !ok || !okContent {
			t.Fatalf("versions answer = %v", answer)
		}
		return int64(version), int64(content)
	}

	if version, content := versions(ask(f.memberID, item.ID, http.StatusOK)); version != item.Version || content != item.Version {
		t.Fatalf("new file = version %d, content %d, want both %d", version, content, item.Version)
	}
	if by := ask(f.memberID, item.ID, http.StatusOK)["modified_by"]; by != f.ownerID {
		t.Fatalf("new file modified_by = %v, want the owner %s", by, f.ownerID)
	}

	renamed := driveJSON[driveTestItem](t, f.call(f.owner, http.MethodPatch, path, item.Version, map[string]any{"name": "board-2.bin"}), http.StatusOK)
	if version, content := versions(ask(f.ownerID, item.ID, http.StatusOK)); version != renamed.Version || content != item.Version {
		t.Fatalf("renamed file = version %d, content %d, want %d and %d", version, content, renamed.Version, item.Version)
	}

	uploadID, sha := f.upload(f.member, f.memberID, []byte("second"))
	changed := driveJSON[driveTestItem](t, f.call(f.member, http.MethodPatch, path, renamed.Version, map[string]any{
		"sha256": sha, "size": 6, "upload_id": uploadID, "mtime_ms": time.Now().UnixMilli(),
	}), http.StatusOK)
	if version, content := versions(ask(f.ownerID, item.ID, http.StatusOK)); version != changed.Version || content != changed.Version {
		t.Fatalf("changed file = version %d, content %d, want both %d", version, content, changed.Version)
	}
	if by := ask(f.ownerID, item.ID, http.StatusOK)["modified_by"]; by != f.memberID {
		t.Fatalf("changed file modified_by = %v, want the member who wrote it %s", by, f.memberID)
	}

	outsider, err := f.database.CreateUser("versions-outsider-"+strconv.FormatInt(time.Now().UnixNano(), 10)+"@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if refused := ask(outsider.ID, item.ID, http.StatusForbidden); refused["code"] == nil {
		t.Fatalf("an outsider's versions answer = %v, want a refusal with a code", refused)
	}
	if missing := ask(f.ownerID, "drv_00000000-0000-4000-8000-000000000000", http.StatusNotFound); missing["code"] != "not_found" {
		t.Fatalf("a missing item's versions answer = %v", missing)
	}

	unsigned := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/internal/journal/file-versions",
		bytes.NewReader([]byte(`{"space_id":"`+f.spaceID+`","item_id":"`+item.ID+`","user_id":"`+f.ownerID+`"}`)))
	request.Header.Set("Content-Type", "application/json")
	f.router.ServeHTTP(unsigned, request)
	if unsigned.Code != http.StatusUnauthorized {
		t.Fatalf("an unsigned versions call = %d, want 401", unsigned.Code)
	}
	forged := signedCallback(t, []byte("not the secret, not the secret!!"), "/internal/journal/file-versions", map[string]any{
		"space_id": f.spaceID, "item_id": item.ID, "user_id": f.ownerID,
	})
	wrong := httptest.NewRecorder()
	f.router.ServeHTTP(wrong, forged)
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("a wrongly signed versions call = %d, want 401", wrong.Code)
	}
}

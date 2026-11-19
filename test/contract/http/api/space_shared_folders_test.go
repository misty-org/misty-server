package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/misty-org/misty-server/internal/platform/db"
	. "github.com/misty-org/misty-server/internal/platform/httpapi"
)

type folderTestFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	URL    string `json:"url,omitempty"`
}

type folderTestUpload struct {
	SHA256  string            `json:"sha256"`
	Size    int64             `json:"size"`
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers"`
}

type folderTestDiff struct {
	Upload []folderTestUpload `json:"upload"`
	Remove []string           `json:"remove"`
	Reused int                `json:"reused"`
}

type folderTestFolder struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	UploadedBy struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"uploadedBy"`
	UploadedAt string `json:"uploadedAt"`
	ByteSize   int64  `json:"byteSize"`
	FileCount  int    `json:"fileCount"`
}

func folderFile(path, content string) (folderTestFile, []byte) {
	sum := sha256.Sum256([]byte(content))
	return folderTestFile{Path: path, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(content))}, []byte(content)
}

func newSharedFolderTestRouter(library *SpaceLibraryService) *chi.Mux {
	router := chi.NewRouter()
	router.Get("/spaces/{spaceID}/folders", library.SharedFolders())
	router.Post("/spaces/{spaceID}/folders", library.SharedFolders())
	router.Delete("/spaces/{spaceID}/folders/{folderID}", library.DeleteSharedFolder())
	router.Post("/spaces/{spaceID}/folders/{folderID}/diff", library.SharedFolderDiff())
	router.Post("/spaces/{spaceID}/folders/{folderID}/commit", library.SharedFolderCommit())
	router.Get("/spaces/{spaceID}/folders/{folderID}/manifest", library.SharedFolderManifest())
	router.Post("/spaces/{spaceID}/library/uploads/{uploadID}/finalize", library.FinalizeUpload())
	return router
}

// rawFolderRequest sends bytes with the given headers, signed in as token.
func rawFolderRequest(t *testing.T, router *chi.Mux, method, target, token string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: token})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func decodeFolderJSON[T any](t *testing.T, response *httptest.ResponseRecorder, want int) T {
	t.Helper()
	var out T
	if response.Code != want {
		t.Fatalf("status = %d, want %d: %s", response.Code, want, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s: %v", response.Body.String(), err)
	}
	return out
}

func folderErrorCode(t *testing.T, response *httptest.ResponseRecorder, want int) string {
	t.Helper()
	return decodeFolderJSON[map[string]any](t, response, want)["code"].(string)
}

// folderBlobState reads a blob's lifecycle state by digest in the Space's domain.
func folderBlobState(t *testing.T, database *db.Database, spaceID, sha string) (state, key string) {
	t.Helper()
	err := database.SpaceTx(context.Background(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(context.Background(), `SELECT b.lifecycle_state,b.r2_object_key FROM library_blobs b
			JOIN spaces s ON s.security_domain_id=b.security_domain_id WHERE s.id=$1 AND b.sha256=$2
			ORDER BY b.created_at DESC LIMIT 1`, spaceID, sha).Scan(&state, &key)
	})
	if err != nil {
		t.Fatalf("read blob %s: %v", sha, err)
	}
	return state, key
}

func personalUsage(t *testing.T, database *db.Database, userID, spaceID string) (used, reserved int64) {
	t.Helper()
	usage, err := database.SpaceStorageUsage(context.Background(), userID, spaceID)
	if err != nil {
		t.Fatal(err)
	}
	return usage.PersonalUsedBytes, usage.PersonalReservedBytes
}

// putFolderBlob stores bytes at the caller's pending upload for sha, as the
// app's presigned PUT straight to R2 would.
func putFolderBlob(t *testing.T, database *db.Database, store LibraryObjectStore, userID, spaceID, folderID, sha string, data []byte) {
	t.Helper()
	ctx := context.Background()
	pending, err := database.PendingSharedFolderUploads(ctx, userID, spaceID, folderID)
	if err != nil {
		t.Fatal(err)
	}
	for _, upload := range pending {
		if upload.SHA256 == sha {
			if err := store.Put(ctx, upload.ObjectKey, bytes.NewReader(data), LibraryObjectMetadata{ByteSize: int64(len(data)), SHA256: sha, MIMEType: db.SharedFolderMIMEType}); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatalf("no pending upload for %s", sha)
}

// A folder's life over signed R2 transfers: a member uploads it, another
// re-uploads only what changed, others read its manifest, its bytes count
// against whoever committed last, and none of them pass through the server.
func TestSharedFolderRoutesUploadReuploadAndDelete(t *testing.T) {
	database := openPresenceTestDatabase(t)
	ctx := context.Background()
	store := NewMemoryLibraryObjectStore()
	library, err := NewSpaceLibraryService(database, store, true, DefaultUploadLimits())
	if err != nil {
		t.Fatal(err)
	}
	library.Presigner = &stubPresigner{}
	router := newSharedFolderTestRouter(library)

	owner, err := database.CreateUser(uniqueTestEmail("folder-owner"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	member, err := database.CreateUser(uniqueTestEmail("folder-member"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	outsider, err := database.CreateUser(uniqueTestEmail("folder-outsider"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.TestingCreateSpace(ctx, owner.ID, "Folders")
	if err != nil {
		t.Fatal(err)
	}
	invite, err := database.TestingInviteToSpace(ctx, owner.ID, space.ID, member.Email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RespondToSpaceInvite(ctx, member.ID, invite.ID, true); err != nil {
		t.Fatal(err)
	}
	ownerToken := newConversationTestBearerToken(t, database, owner.ID)
	memberToken := newConversationTestBearerToken(t, database, member.ID)
	outsiderToken := newConversationTestBearerToken(t, database, outsider.ID)
	base := "/spaces/" + space.ID + "/folders"
	call := func(token, method, target string, body any) *httptest.ResponseRecorder {
		return performConversationRequest(t, router, method, target, token, body)
	}

	if code := folderErrorCode(t, call(outsiderToken, http.MethodGet, base, nil), http.StatusForbidden); code != "forbidden" {
		t.Fatalf("outsider list code = %s", code)
	}
	for _, name := range []string{"", "   ", strings.Repeat("n", 121)} {
		folderErrorCode(t, call(ownerToken, http.MethodPost, base, map[string]any{"name": name}), http.StatusBadRequest)
	}
	folder := decodeFolderJSON[folderTestFolder](t, call(ownerToken, http.MethodPost, base, map[string]any{"name": "  Design assets "}), http.StatusCreated)
	if folder.Name != "Design assets" || folder.UploadedBy.ID != owner.ID || folder.UploadedBy.Name == "" || folder.ByteSize != 0 || folder.FileCount != 0 || folder.UploadedAt == "" {
		t.Fatalf("created folder = %+v", folder)
	}
	listed := decodeFolderJSON[struct {
		Folders []folderTestFolder `json:"folders"`
	}](t, call(memberToken, http.MethodGet, base, nil), http.StatusOK)
	if len(listed.Folders) != 1 || listed.Folders[0].ID != folder.ID {
		t.Fatalf("listed = %+v", listed)
	}
	folderPath := base + "/" + folder.ID

	hello, helloBytes := folderFile("a.txt", "hello")
	world, worldBytes := folderFile("dir/b.bin", "world!!")
	empty, _ := folderFile("empty.txt", "")
	dup, _ := folderFile("dup.txt", "hello")
	manifest := []folderTestFile{world, hello, empty, dup}
	content := map[string][]byte{hello.SHA256: helloBytes, world.SHA256: worldBytes}

	// Paths are relative posix paths; digests and sizes must be well formed.
	for name, files := range map[string][]folderTestFile{
		"dot-dot":       {{Path: "../x", SHA256: hello.SHA256, Size: 5}},
		"absolute":      {{Path: "/x", SHA256: hello.SHA256, Size: 5}},
		"backslash":     {{Path: `a\b`, SHA256: hello.SHA256, Size: 5}},
		"empty segment": {{Path: "a//b", SHA256: hello.SHA256, Size: 5}},
		"too long":      {{Path: strings.Repeat("p", 1025), SHA256: hello.SHA256, Size: 5}},
		"duplicate":     {hello, hello},
		"file and dir":  {{Path: "dir", SHA256: hello.SHA256, Size: 5}, world},
		"bad digest":    {{Path: "x", SHA256: "abc", Size: 5}},
		"empty digest":  {{Path: "x", SHA256: hello.SHA256, Size: 0}},
	} {
		if code := folderErrorCode(t, call(ownerToken, http.MethodPost, folderPath+"/diff", map[string]any{"files": files}), http.StatusBadRequest); code != "invalid_request" {
			t.Fatalf("%s: code = %s", name, code)
		}
	}
	folderErrorCode(t, call(outsiderToken, http.MethodPost, folderPath+"/diff", map[string]any{"files": manifest}), http.StatusForbidden)
	folderErrorCode(t, call(ownerToken, http.MethodGet, base+"/not-a-folder/manifest", nil), http.StatusNotFound)

	usedBefore, _ := personalUsage(t, database, owner.ID, space.ID)
	diff := decodeFolderJSON[folderTestDiff](t, call(ownerToken, http.MethodPost, folderPath+"/diff", map[string]any{"files": manifest}), http.StatusOK)
	if len(diff.Upload) != 2 || diff.Reused != 1 || len(diff.Remove) != 0 {
		t.Fatalf("first diff = %+v", diff)
	}
	if _, reserved := personalUsage(t, database, owner.ID, space.ID); reserved != 12 {
		t.Fatalf("reserved after diff = %d, want 12", reserved)
	}
	missing := decodeFolderJSON[map[string]any](t, call(ownerToken, http.MethodPost, folderPath+"/commit", map[string]any{"files": manifest}), http.StatusConflict)
	if missing["code"] != "folder_blobs_missing" || len(missing["missing"].([]any)) != 2 {
		t.Fatalf("commit before upload = %v", missing)
	}
	for _, upload := range diff.Upload {
		if !strings.HasPrefix(upload.URL, "https://") || upload.Method != http.MethodPut || upload.Headers[LibraryUploadTokenHeader] != "" {
			t.Fatalf("signed upload target = %+v", upload)
		}
		putFolderBlob(t, database, store, owner.ID, space.ID, folder.ID, upload.SHA256, content[upload.SHA256])
	}

	committed := decodeFolderJSON[folderTestFolder](t, call(ownerToken, http.MethodPost, folderPath+"/commit", map[string]any{"files": manifest}), http.StatusOK)
	if committed.ByteSize != 17 || committed.FileCount != 4 || committed.UploadedBy.ID != owner.ID {
		t.Fatalf("committed = %+v", committed)
	}
	if used, reserved := personalUsage(t, database, owner.ID, space.ID); used-usedBefore != 17 || reserved != 0 {
		t.Fatalf("owner usage after commit = +%d used, %d reserved; want +17, 0", used-usedBefore, reserved)
	}

	read := decodeFolderJSON[struct {
		Folder folderTestFolder `json:"folder"`
		Files  []folderTestFile `json:"files"`
	}](t, call(memberToken, http.MethodGet, folderPath+"/manifest", nil), http.StatusOK)
	paths := []string{}
	for _, file := range read.Files {
		paths = append(paths, file.Path)
	}
	if read.Folder.ID != folder.ID || strings.Join(paths, ",") != "a.txt,dir/b.bin,dup.txt,empty.txt" {
		t.Fatalf("manifest = %+v", read)
	}
	for _, file := range read.Files {
		if file.Size == 0 {
			if file.URL != "" {
				t.Fatalf("empty file URL = %q", file.URL)
			}
			continue
		}
		if !strings.HasPrefix(file.URL, "https://") || !strings.Contains(file.URL, "X-Amz-Signature") {
			t.Fatalf("signed download URL = %q", file.URL)
		}
	}
	folderErrorCode(t, call(outsiderToken, http.MethodGet, folderPath+"/manifest", nil), http.StatusForbidden)

	// The member re-uploads: one file changed, one removed. Only the change is sent.
	_, worldKey := folderBlobState(t, database, space.ID, world.SHA256)
	changed, changedBytes := folderFile("dir/b.bin", "changed")
	second := []folderTestFile{hello, changed, empty}
	memberBefore, _ := personalUsage(t, database, member.ID, space.ID)
	diff = decodeFolderJSON[folderTestDiff](t, call(memberToken, http.MethodPost, folderPath+"/diff", map[string]any{"files": second}), http.StatusOK)
	if len(diff.Upload) != 1 || diff.Upload[0].SHA256 != changed.SHA256 || diff.Reused != 2 || strings.Join(diff.Remove, ",") != "dup.txt" {
		t.Fatalf("second diff = %+v", diff)
	}
	putFolderBlob(t, database, store, member.ID, space.ID, folder.ID, changed.SHA256, changedBytes)
	committed = decodeFolderJSON[folderTestFolder](t, call(memberToken, http.MethodPost, folderPath+"/commit", map[string]any{"files": second}), http.StatusOK)
	if committed.ByteSize != 12 || committed.FileCount != 3 || committed.UploadedBy.ID != member.ID {
		t.Fatalf("second commit = %+v", committed)
	}
	if used, _ := personalUsage(t, database, owner.ID, space.ID); used != usedBefore {
		t.Fatalf("owner usage after the member's commit = %d, want %d", used, usedBefore)
	}
	if used, _ := personalUsage(t, database, member.ID, space.ID); used-memberBefore != 12 {
		t.Fatalf("member usage after commit = +%d, want +12", used-memberBefore)
	}
	if state, _ := folderBlobState(t, database, space.ID, world.SHA256); state != "deleted" {
		t.Fatalf("replaced blob state = %s, want deleted", state)
	}
	if _, err := store.Head(ctx, worldKey); err != ErrLibraryObjectNotFound {
		t.Fatalf("replaced object Head = %v, want not found", err)
	}
	if state, _ := folderBlobState(t, database, space.ID, hello.SHA256); state != "ready" {
		t.Fatalf("kept blob state = %s, want ready", state)
	}

	// Room is checked at the diff, against the existing storage limits.
	limit := int64(20)
	if err := database.SetAccountLimits(ctx, member.ID, db.AccountLimits{StorageBytes: &limit}); err != nil {
		t.Fatal(err)
	}
	big, _ := folderFile("big.bin", strings.Repeat("x", 100))
	if code := folderErrorCode(t, call(memberToken, http.MethodPost, folderPath+"/diff", map[string]any{"files": append(second, big)}), http.StatusConflict); code != "owner_storage_quota_exceeded" {
		t.Fatalf("over-limit diff code = %s", code)
	}
	if err := database.SetAccountLimits(ctx, member.ID, db.AccountLimits{}); err != nil {
		t.Fatal(err)
	}

	if got := call(memberToken, http.MethodDelete, folderPath, nil); got.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", got.Code, got.Body.String())
	}
	folderErrorCode(t, call(memberToken, http.MethodGet, folderPath+"/manifest", nil), http.StatusNotFound)
	listed = decodeFolderJSON[struct {
		Folders []folderTestFolder `json:"folders"`
	}](t, call(ownerToken, http.MethodGet, base, nil), http.StatusOK)
	if len(listed.Folders) != 0 {
		t.Fatalf("listed after delete = %+v", listed)
	}
	if used, _ := personalUsage(t, database, member.ID, space.ID); used != memberBefore {
		t.Fatalf("member usage after delete = %d, want %d", used, memberBefore)
	}
	if state, _ := folderBlobState(t, database, space.ID, hello.SHA256); state != "deleted" {
		t.Fatalf("blob of a deleted folder = %s, want deleted", state)
	}
}

// With R2 the diff hands out presigned PUTs, a re-diff skips what already
// landed, and the manifest hands out presigned GETs.
func TestSharedFolderRoutesUseSignedTransfersWithR2(t *testing.T) {
	database := openPresenceTestDatabase(t)
	ctx := context.Background()
	store := NewMemoryLibraryObjectStore()
	library, err := NewSpaceLibraryService(database, store, true, DefaultUploadLimits())
	if err != nil {
		t.Fatal(err)
	}
	library.Presigner = &stubPresigner{}
	router := newSharedFolderTestRouter(library)
	owner, err := database.CreateUser(uniqueTestEmail("folder-r2"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.TestingCreateSpace(ctx, owner.ID, "Signed folders")
	if err != nil {
		t.Fatal(err)
	}
	token := newConversationTestBearerToken(t, database, owner.ID)
	call := func(method, target string, body any) *httptest.ResponseRecorder {
		return performConversationRequest(t, router, method, target, token, body)
	}
	folder := decodeFolderJSON[folderTestFolder](t, call(http.MethodPost, "/spaces/"+space.ID+"/folders", map[string]any{"name": "Signed"}), http.StatusCreated)
	folderPath := "/spaces/" + space.ID + "/folders/" + folder.ID
	one, oneBytes := folderFile("one.txt", "first file")
	two, twoBytes := folderFile("nested/two.txt", "second file")
	manifest := []folderTestFile{one, two}

	diff := decodeFolderJSON[folderTestDiff](t, call(http.MethodPost, folderPath+"/diff", map[string]any{"files": manifest}), http.StatusOK)
	if len(diff.Upload) != 2 || diff.Reused != 0 {
		t.Fatalf("diff = %+v", diff)
	}
	for _, upload := range diff.Upload {
		if !strings.HasPrefix(upload.URL, "https://") || upload.Headers[LibraryUploadTokenHeader] != "" {
			t.Fatalf("signed upload target = %+v", upload)
		}
	}
	// The client PUTs the first blob straight to R2, then asks again; each PUT
	// goes to the upload the latest diff handed out.
	put := func(sha string, data []byte) {
		t.Helper()
		putFolderBlob(t, database, store, owner.ID, space.ID, folder.ID, sha, data)
	}
	put(one.SHA256, oneBytes)
	diff = decodeFolderJSON[folderTestDiff](t, call(http.MethodPost, folderPath+"/diff", map[string]any{"files": manifest}), http.StatusOK)
	if len(diff.Upload) != 1 || diff.Upload[0].SHA256 != two.SHA256 || diff.Reused != 1 {
		t.Fatalf("re-diff = %+v", diff)
	}
	put(two.SHA256, twoBytes)
	committed := decodeFolderJSON[folderTestFolder](t, call(http.MethodPost, folderPath+"/commit", map[string]any{"files": manifest}), http.StatusOK)
	if committed.ByteSize != int64(len(oneBytes)+len(twoBytes)) || committed.FileCount != 2 {
		t.Fatalf("committed = %+v", committed)
	}
	read := decodeFolderJSON[struct {
		Files []folderTestFile `json:"files"`
	}](t, call(http.MethodGet, folderPath+"/manifest", nil), http.StatusOK)
	for _, file := range read.Files {
		if !strings.HasPrefix(file.URL, "https://") || !strings.Contains(file.URL, "X-Amz-Signature") {
			t.Fatalf("signed download URL = %q", file.URL)
		}
	}
	// Nothing is left to send for an unchanged folder.
	diff = decodeFolderJSON[folderTestDiff](t, call(http.MethodPost, folderPath+"/diff", map[string]any{"files": manifest}), http.StatusOK)
	if len(diff.Upload) != 0 || diff.Reused != 2 || len(diff.Remove) != 0 {
		t.Fatalf("unchanged diff = %+v", diff)
	}
}

// Without a store that can sign R2 URLs, a folder's bytes have nowhere to go
// but through the server, so its transfers are refused instead.
func TestSharedFolderTransfersNeedSignedR2(t *testing.T) {
	database := openPresenceTestDatabase(t)
	ctx := context.Background()
	library, err := NewSpaceLibraryService(database, NewMemoryLibraryObjectStore(), true, DefaultUploadLimits())
	if err != nil {
		t.Fatal(err)
	}
	router := newSharedFolderTestRouter(library)
	owner, err := database.CreateUser(uniqueTestEmail("folder-unsigned"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.TestingCreateSpace(ctx, owner.ID, "Unsigned folders")
	if err != nil {
		t.Fatal(err)
	}
	token := newConversationTestBearerToken(t, database, owner.ID)
	call := func(method, target string, body any) *httptest.ResponseRecorder {
		return performConversationRequest(t, router, method, target, token, body)
	}
	folder := decodeFolderJSON[folderTestFolder](t, call(http.MethodPost, "/spaces/"+space.ID+"/folders", map[string]any{"name": "Unsigned"}), http.StatusCreated)
	folderPath := "/spaces/" + space.ID + "/folders/" + folder.ID
	file, _ := folderFile("a.txt", "hello")
	if code := folderErrorCode(t, call(http.MethodPost, folderPath+"/diff", map[string]any{"files": []folderTestFile{file}}), http.StatusServiceUnavailable); code != "folder_direct_transfer_unavailable" {
		t.Fatalf("unsigned diff code = %s", code)
	}
	if code := folderErrorCode(t, call(http.MethodGet, folderPath+"/manifest", nil), http.StatusServiceUnavailable); code != "folder_direct_transfer_unavailable" {
		t.Fatalf("unsigned manifest code = %s", code)
	}
}

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
	"net/url"
	"strconv"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/misty-org/misty-server/internal/platform/db"
	. "github.com/misty-org/misty-server/internal/platform/httpapi"
)

// The Space drive's contract tests share this fixture: a Space with its
// owner, a member and an outsider, the drive's routes over an in-memory
// object store with a stub presigner, and helpers that act as one computer.

type driveTestItem struct {
	ID             string  `json:"id"`
	SpaceID        string  `json:"space_id"`
	ParentID       *string `json:"parent_id"`
	Name           string  `json:"name"`
	Kind           string  `json:"kind"`
	SHA256         *string `json:"sha256"`
	Size           int64   `json:"size"`
	MTimeMS        int64   `json:"mtime_ms"`
	Version        int64   `json:"version"`
	Seq            int64   `json:"seq"`
	ModifiedBy     string  `json:"modified_by"`
	ModifiedByName string  `json:"modified_by_name"`
	Trashed        bool    `json:"trashed"`
	TrashHold      bool    `json:"trash_hold"`
	TrashedBy      *string `json:"trashed_by"`
	Deleted        bool    `json:"deleted"`
	ReplacedOf     *string `json:"replaced_of"`
	// Revived and Replaced are only in the answers to writes.
	Revived  bool           `json:"revived"`
	Replaced *driveTestItem `json:"replaced"`
}

type driveTestChanges struct {
	Items     []driveTestItem `json:"items"`
	NextSince int64           `json:"next_since"`
	More      bool            `json:"more"`
	Head      int64           `json:"head"`
}

// driveTestDevice is one computer of one person.
type driveTestDevice struct {
	token, id, name string
}

type driveFixture struct {
	t                      *testing.T
	ctx                    context.Context
	database               *db.Database
	store                  *MemoryLibraryObjectStore
	drive                  *SpaceDriveService
	router                 *chi.Mux
	spaceID                string
	ownerID, memberID      string
	owner, member, memberB driveTestDevice
	outsider               driveTestDevice
	base                   string
}

func newDriveTestRouter(drive *SpaceDriveService) *chi.Mux {
	router := chi.NewRouter()
	base := "/spaces/{spaceID}/drive"
	item := base + "/items/{itemID}"
	router.Get(base+"/changes", drive.DriveChanges())
	router.Get(base+"/trash", drive.DriveTrash())
	router.Get(base+"/in-use", drive.DriveInUseMarks())
	router.Post(base+"/uploads", drive.StartDriveUpload())
	router.Post(base+"/uploads/{uploadID}/complete", drive.CompleteDriveUpload())
	router.Post(base+"/items", drive.CreateDriveItem())
	router.Get(item, drive.DriveItem())
	router.Patch(item, drive.PatchDriveItem())
	router.Delete(item, drive.PurgeDriveItem())
	router.Get(item+"/content", drive.DriveContent())
	router.Post(item+"/trash", drive.TrashDriveItem())
	router.Post(item+"/restore", drive.RestoreDriveItem())
	router.Post(item+"/check-in-use", drive.CheckDriveInUse())
	router.Put(item+"/in-use", drive.MarkDriveInUse())
	router.Delete(item+"/in-use", drive.ClearDriveInUse())
	return router
}

func newDriveFixture(t *testing.T, label string) *driveFixture {
	t.Helper()
	f := &driveFixture{t: t, ctx: context.Background(), database: openPresenceTestDatabase(t), store: NewMemoryLibraryObjectStore()}
	drive, err := NewSpaceDriveService(f.database, f.store)
	if err != nil {
		t.Fatal(err)
	}
	drive.Presigner = &stubPresigner{}
	f.drive, f.router = drive, newDriveTestRouter(drive)
	user := func(name string) string {
		created, err := f.database.CreateUser(uniqueTestEmail(label+"-"+name), "password123")
		if err != nil {
			t.Fatal(err)
		}
		return created.ID
	}
	f.ownerID, f.memberID = user("owner"), user("member")
	outsiderID := user("outsider")
	space, err := f.database.TestingCreateSpace(f.ctx, f.ownerID, "Drive "+label)
	if err != nil {
		t.Fatal(err)
	}
	f.spaceID, f.base = space.ID, "/spaces/"+space.ID+"/drive"
	var memberEmail string
	if err := f.database.SpaceTx(f.ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(f.ctx, `SELECT email FROM users WHERE id=$1`, f.memberID).Scan(&memberEmail)
	}); err != nil {
		t.Fatal(err)
	}
	invite, err := f.database.TestingInviteToSpace(f.ctx, f.ownerID, space.ID, memberEmail)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.RespondToSpaceInvite(f.ctx, f.memberID, invite.ID, true); err != nil {
		t.Fatal(err)
	}
	device := func(userID, name string) driveTestDevice {
		return driveTestDevice{token: newConversationTestBearerToken(t, f.database, userID), id: "dev_" + uuid.NewString(), name: name}
	}
	f.owner, f.member, f.memberB = device(f.ownerID, "Ana’s MacBook"), device(f.memberID, "Ben's laptop"), device(f.memberID, "Ben's desktop")
	f.outsider = device(outsiderID, "Somewhere")
	return f
}

// call sends one request from a computer. version, when above 0, goes in If-Match.
func (f *driveFixture) call(from driveTestDevice, method, target string, version int64, body any) *httptest.ResponseRecorder {
	f.t.Helper()
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			f.t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, target, bytes.NewReader(payload))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if from.id != "" {
		req.Header.Set(DriveDeviceHeader, from.id)
		req.Header.Set(DriveDeviceNameHeader, url.PathEscape(from.name))
	}
	if version > 0 {
		req.Header.Set("If-Match", strconv.FormatInt(version, 10))
	}
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: from.token})
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

func driveJSON[T any](t *testing.T, response *httptest.ResponseRecorder, want int) T {
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

func driveCode(t *testing.T, response *httptest.ResponseRecorder, want int) string {
	t.Helper()
	code, _ := driveJSON[map[string]any](t, response, want)["code"].(string)
	return code
}

func (f *driveFixture) folder(from driveTestDevice, parent *string, name string) driveTestItem {
	f.t.Helper()
	return driveJSON[driveTestItem](f.t, f.call(from, http.MethodPost, f.base+"/items", 0,
		map[string]any{"parent_id": parent, "name": name, "kind": "folder"}), http.StatusCreated)
}

// upload sends content as the app would: ask, PUT straight to "R2", complete.
// It returns the upload to name in the item.
func (f *driveFixture) upload(from driveTestDevice, userID string, content []byte) (string, string) {
	f.t.Helper()
	sum := sha256.Sum256(content)
	sha := hex.EncodeToString(sum[:])
	answer := driveJSON[map[string]any](f.t, f.call(from, http.MethodPost, f.base+"/uploads", 0,
		map[string]any{"sha256": sha, "size": len(content)}), http.StatusOK)
	uploadID := answer["upload_id"].(string)
	if answer["present"] == true {
		return uploadID, sha
	}
	single, ok := answer["single"].(map[string]any)
	if !ok || single["method"] != http.MethodPut || single["url"] == "" {
		f.t.Fatalf("upload answer = %v", answer)
	}
	upload, err := f.database.DriveUploadByID(f.ctx, userID, f.spaceID, uploadID)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.store.Put(f.ctx, upload.ObjectKey, bytes.NewReader(content), LibraryObjectMetadata{
		ByteSize: int64(len(content)), SHA256: sha, MIMEType: db.DriveMIMEType,
	}); err != nil {
		f.t.Fatal(err)
	}
	driveJSON[map[string]any](f.t, f.call(from, http.MethodPost, f.base+"/uploads/"+uploadID+"/complete", 0, map[string]any{}), http.StatusOK)
	return uploadID, sha
}

// file uploads content and makes a file of it.
func (f *driveFixture) file(from driveTestDevice, userID string, parent *string, name, content string) driveTestItem {
	f.t.Helper()
	uploadID, sha := f.upload(from, userID, []byte(content))
	return driveJSON[driveTestItem](f.t, f.call(from, http.MethodPost, f.base+"/items", 0, map[string]any{
		"parent_id": parent, "name": name, "kind": "file", "sha256": sha, "size": len(content), "mtime_ms": 1700000000000, "upload_id": uploadID,
	}), http.StatusCreated)
}

func (f *driveFixture) changes(from driveTestDevice, since int64) driveTestChanges {
	f.t.Helper()
	return driveJSON[driveTestChanges](f.t, f.call(from, http.MethodGet, f.base+"/changes?since="+strconv.FormatInt(since, 10), 0, nil), http.StatusOK)
}

func (f *driveFixture) exec(query string, args ...any) {
	f.t.Helper()
	if err := f.database.SpaceTx(f.ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(f.ctx, query, args...)
		return err
	}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *driveFixture) text(query string, args ...any) string {
	f.t.Helper()
	var out sql.NullString
	if err := f.database.SpaceTx(f.ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(f.ctx, query, args...).Scan(&out)
	}); err != nil {
		f.t.Fatal(err)
	}
	return out.String
}

func (f *driveFixture) itemPath(id string) string { return f.base + "/items/" + id }

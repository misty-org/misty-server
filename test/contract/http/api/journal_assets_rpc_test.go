package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/appcatalog"
	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

// Exercise the public asset RPC routes through the actual domain handlers. Only
// object storage/signing is local: memberships, grants, quota and files use SQL.
func TestJournalAssetRPCBindsUploadParentAndHostCredential(t *testing.T) {
	database := openPresenceTestDatabase(t)
	user, err := database.CreateUserWithUsername("Journal assets", "assets_"+uuid.NewString()[:8], uniqueTestEmail("journal-assets"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(t.Context(), user.ID, "Journal asset RPC")
	if err != nil {
		t.Fatal(err)
	}
	catalog, ok := appcatalog.Find("journal")
	if !ok {
		t.Fatal("missing Journal catalog")
	}
	if _, err := database.InstallUserApp(t.Context(), user.ID, "journal", catalog.Version, catalog.PermissionVersion, catalog.Scopes); err != nil {
		t.Fatal(err)
	}
	token := "journal-rpc-" + uuid.NewString()
	if _, err := database.CreateAppRuntimeSession(t.Context(), user.ID, "journal", security.HashToken(token), space.ID, db.AppRuntimeSessionTTL); err != nil {
		t.Fatal(err)
	}
	store := NewMemoryLibraryObjectStore()
	service, err := NewSpaceLibraryService(database, store, true, DefaultUploadLimits())
	if err != nil {
		t.Fatal(err)
	}
	presigner := &stubPresigner{}
	service.TestingPresigner = presigner
	service.SetNoteAssetsEnabled(true)
	service.SetDrawingAssetsEnabled(true)
	router := chi.NewRouter()
	router.Post("/spaces/{spaceID}/notes/{noteID}/assets/uploads", service.SpaceNoteAssets())
	router.Post("/spaces/{spaceID}/drawings/{drawingID}/assets/uploads", service.SpaceDrawingAssets())
	router.Post("/spaces/{spaceID}/notes/{noteID}/assets/uploads/{uploadID}/finalize", service.FinalizeUpload())
	router.Post("/spaces/{spaceID}/drawings/{drawingID}/assets/uploads/{uploadID}/finalize", service.FinalizeUpload())
	router.Get("/spaces/{spaceID}/notes/{noteID}/assets/{assetID}/download", service.SpaceNoteAssetDownload())
	router.Get("/spaces/{spaceID}/drawings/{drawingID}/assets/{assetID}/download", service.SpaceDrawingAssetDownload())
	router.Post("/app-runtime/rpc", OfficialAppRPC(database, router, ""))
	call := func(method string, path map[string]string, body any, credential string, want int) map[string]any {
		t.Helper()
		params := map[string]any{"path": path}
		if body != nil {
			params["body"] = body
		}
		encoded, err := json.Marshal(map[string]any{"protocol": 2, "method": method, "params": params})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, "/app-runtime/rpc", bytes.NewReader(encoded))
		request.Header.Set("Authorization", "Bearer "+token)
		if credential != "" {
			request.Header.Set(TestingLibraryUploadTokenHeader, credential)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s returned %d, want %d: %s", method, response.Code, want, response.Body.String())
		}
		var result map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	for _, kind := range []string{"notes", "drawings"} {
		t.Run(kind, func(t *testing.T) {
			var id, otherID, key string
			if kind == "notes" {
				first, err := database.CreateSpaceNote(t.Context(), user.ID, space.ID, "Asset parent")
				if err != nil {
					t.Fatal(err)
				}
				other, err := database.CreateSpaceNote(t.Context(), user.ID, space.ID, "Other parent")
				if err != nil {
					t.Fatal(err)
				}
				id, otherID, key = first.ID, other.ID, "noteID"
			} else {
				first, err := database.CreateSpaceDrawing(t.Context(), user.ID, space.ID, "Asset parent")
				if err != nil {
					t.Fatal(err)
				}
				other, err := database.CreateSpaceDrawing(t.Context(), user.ID, space.ID, "Other parent")
				if err != nil {
					t.Fatal(err)
				}
				id, otherID, key = first.ID, other.ID, "drawingID"
			}
			content := []byte("local raster metadata fixture " + kind)
			digest := sha256.Sum256(content)
			body := map[string]any{"filename": "image.png", "mime_type": "image/png", "byte_size": len(content), "sha256": hex.EncodeToString(digest[:])}
			if kind == "drawings" {
				body["file_id"] = "scene-file"
			}
			reserved := call(kind+".assets.reserve", map[string]string{key: id}, body, "", http.StatusCreated)
			uploadID := reserved["upload"].(map[string]any)["id"].(string)
			credential := reserved["finalize"].(map[string]any)["headers"].(map[string]any)[TestingLibraryUploadTokenHeader].(string)
			if err := store.Put(t.Context(), presigner.putKey, bytes.NewReader(content), presigner.putMetadata); err != nil {
				t.Fatal(err)
			}
			path := map[string]string{key: id, "uploadID": uploadID}
			call(kind+".assets.finalize", path, nil, "", http.StatusForbidden)
			call(kind+".assets.finalize", map[string]string{key: otherID, "uploadID": uploadID}, nil, credential, http.StatusNotFound)
			call(kind+".assets.finalize", path, nil, "bad, duplicate", http.StatusBadRequest)
			if err := database.ValidateJournalUploadTarget(t.Context(), user.ID, space.ID, uploadID, "", ""); err == nil {
				t.Fatal("generic Library route accepted Journal upload")
			}
			result := call(kind+".assets.finalize", path, nil, credential, http.StatusOK)
			assetKey := "note_asset"
			if kind == "drawings" {
				assetKey = "drawing_asset"
			}
			assetID := result[assetKey].(map[string]any)["id"].(string)
			asset := result[assetKey].(map[string]any)
			if asset["mime_type"] != "image/png" || asset["byte_size"] != float64(len(content)) || asset["sha256"] != hex.EncodeToString(digest[:]) {
				t.Fatal("finalized asset omitted verified SDK metadata")
			}
			repeated := call(kind+".assets.finalize", path, nil, credential, http.StatusOK)
			if repeated[assetKey].(map[string]any)["id"] != assetID {
				t.Fatal("finalize retry duplicated asset")
			}
			if repeated[assetKey].(map[string]any)["sha256"] != asset["sha256"] || repeated[assetKey].(map[string]any)["byte_size"] != asset["byte_size"] {
				t.Fatal("finalize retry lost verified SDK metadata")
			}
			download := call(kind+".assets.download", map[string]string{key: id, "assetID": assetID}, nil, "", http.StatusOK)
			if download["url"] == "" || download["sha256"] != hex.EncodeToString(digest[:]) {
				t.Fatal("missing verified download descriptor")
			}
			call(kind+".assets.download", map[string]string{key: otherID, "assetID": assetID}, nil, "", http.StatusNotFound)
			if kind == "notes" {
				if err := database.SetSpaceNoteArchived(t.Context(), user.ID, id, true); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := database.DeleteSpaceDrawing(t.Context(), user.ID, id); err != nil {
					t.Fatal(err)
				}
			}
			// A completed upload is not an authorization bypass on retry.
			call(kind+".assets.finalize", path, nil, credential, http.StatusNotFound)
			if _, err := database.CompleteLibraryUpload(t.Context(), user.ID, space.ID, uploadID,
				security.HashToken(credential), int64(len(content)), hex.EncodeToString(digest[:]), "image/png", nil); err == nil {
				t.Fatal("completion transaction accepted a retired parent")
			}
		})
	}
}

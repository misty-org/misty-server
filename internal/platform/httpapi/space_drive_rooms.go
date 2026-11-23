package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/misty-org/misty-server/internal/platform/db"
)

const (
	fileRoomTextBodyLimit = 9 << 20
	fileRoomParty         = "file-room"
)

var fileRoomDevice = db.DriveDevice{ID: "dev_00000000-0000-4000-8000-00000000f11e", Name: "Live document"}

var plainTextExtensions = map[string]bool{
	".txt": true, ".text": true, ".md": true, ".markdown": true, ".mdx": true, ".rst": true, ".adoc": true,
	".csv": true, ".tsv": true, ".log": true, ".ini": true, ".cfg": true, ".conf": true, ".toml": true,
	".yaml": true, ".yml": true, ".json": true, ".jsonc": true, ".xml": true, ".html": true, ".htm": true,
	".css": true, ".scss": true, ".less": true, ".js": true, ".mjs": true, ".cjs": true, ".jsx": true,
	".ts": true, ".tsx": true, ".py": true, ".rb": true, ".go": true, ".rs": true, ".java": true, ".kt": true,
	".swift": true, ".c": true, ".h": true, ".cc": true, ".cpp": true, ".hpp": true, ".cs": true, ".php": true,
	".sh": true, ".bash": true, ".zsh": true, ".fish": true, ".sql": true, ".lua": true, ".r": true, ".tex": true,
	".vue": true, ".svelte": true, ".graphql": true, ".proto": true,
}

func DriveFileFormat(name string) string {
	if plainTextExtensions[strings.ToLower(path.Ext(name))] {
		return "text"
	}
	return "app"
}

func (s *SpaceDriveService) SetJournalCollab(config JournalCollabConfig) {
	s.JournalCollab = config
}

type fileRoomTicket struct {
	JournalTicket
	Format string `json:"format"`
	ItemID string `json:"item_id"`
}

func (s *SpaceDriveService) DriveFileRoomTicket() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			AppID string `json:"app_id"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		spaceID, itemID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "itemID")
		access, err := s.database.DriveFileRoomAccess(r.Context(), userID, spaceID, itemID, strings.TrimSpace(body.AppID))
		if err != nil {
			writeDriveError(w, err)
			return
		}
		format := DriveFileFormat(access.Item.Name)
		role := "viewer"
		if access.CanWrite {
			role = "editor"
		}
		ticket, err := s.JournalCollab.MintFileRoomTicket(userID, spaceID, access.Item.ID, strings.TrimSpace(body.AppID), format, role)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		writeJSON(w, http.StatusCreated, fileRoomTicket{JournalTicket: ticket, Format: format, ItemID: access.Item.ID})
	}
}

func (s *SpaceDriveService) DriveFileRoomText() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, fileRoomTextBodyLimit+1))
		if err != nil || len(body) > fileRoomTextBodyLimit {
			writeSpaceError(w, db.ErrSpaceInvalid)
			return
		}
		timestamp := r.Header.Get("X-Misty-Timestamp")
		issuedAt, parseErr := strconv.ParseInt(timestamp, 10, 64)
		if parseErr != nil || time.Since(time.Unix(issuedAt, 0)).Abs() > 5*time.Minute ||
			!s.JournalCollab.VerifyProjectionSignature(timestamp, body, r.Header.Get("X-Misty-Signature")) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "unauthorized"})
			return
		}
		var input struct {
			SpaceID     string `json:"space_id"`
			ItemID      string `json:"item_id"`
			UserID      string `json:"user_id"`
			Text        string `json:"text"`
			BaseVersion int64  `json:"base_version"`
		}
		if json.Unmarshal(body, &input) != nil || input.SpaceID == "" || input.ItemID == "" || input.UserID == "" ||
			!utf8.ValidString(input.Text) {
			writeSpaceError(w, db.ErrSpaceInvalid)
			return
		}
		item, err := s.saveFileRoomText(r.Context(), input.UserID, input.SpaceID, input.ItemID, input.BaseVersion, []byte(input.Text))
		if err != nil {
			writeDriveError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"item_id": item.ID, "version": item.Version})
	}
}

func (s *SpaceDriveService) saveFileRoomText(ctx context.Context, userID, spaceID, itemID string, base int64, content []byte) (*db.DriveItem, error) {
	item, err := s.database.DriveItem(ctx, userID, spaceID, itemID)
	if err != nil {
		return nil, err
	}
	if item.Kind != "file" || item.Trashed || item.Deleted || DriveFileFormat(item.Name) != "text" {
		return nil, db.ErrDriveNotFound
	}
	digest := sha256.Sum256(content)
	sha := hex.EncodeToString(digest[:])
	if item.SHA256 != nil && *item.SHA256 == sha {
		return item, nil
	}
	size := int64(len(content))
	if size > 0 {
		key := db.NewDriveObjectKey()
		if err := s.Store.Put(ctx, key, bytes.NewReader(content), LibraryObjectMetadata{
			ByteSize: size, SHA256: sha, MIMEType: db.DriveMIMEType,
		}); err != nil {
			return nil, err
		}
		kept, err := s.database.EnsureDriveBlob(ctx, spaceID, key, sha, size)
		if err != nil {
			s.cleanUpDriveObjects(ctx, []string{key}, nil)
			return nil, err
		}
		if kept != key {
			s.cleanUpDriveObjects(ctx, []string{key}, nil)
		}
	}
	if base < 1 || base > item.Version {
		base = item.Version
	}
	result, err := s.database.PatchDriveItem(ctx, db.DrivePatchInput{
		UserID: userID, SpaceID: spaceID, ItemID: item.ID, Device: fileRoomDevice,
		BaseVersion: base, Content: true, SHA256: sha, Size: size,
	})
	if err != nil {
		return nil, err
	}
	s.cleanUpDriveObjects(ctx, result.DeleteObjectKeys, result.RetireKeys)
	return &result.Item, nil
}

package api

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	"github.com/coregx/gxpdf"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/platform/security"
	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

type docxTextNode struct {
	Text string `xml:",chardata"`
}

func extractDOCXText(data []byte) (string, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	for _, file := range reader.File {
		if file.Name != "word/document.xml" {
			continue
		}
		stream, err := file.Open()
		if err != nil {
			return "", err
		}
		decoder := xml.NewDecoder(io.LimitReader(stream, 25_000_001))
		var builder strings.Builder
		for {
			token, err := decoder.Token()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				stream.Close()
				return "", err
			}
			switch value := token.(type) {
			case xml.StartElement:
				if value.Name.Local == "t" {
					var node docxTextNode
					if err := decoder.DecodeElement(&node, &value); err != nil {
						stream.Close()
						return "", err
					}
					builder.WriteString(node.Text)
				} else if value.Name.Local == "p" || value.Name.Local == "br" {
					builder.WriteByte('\n')
				}
			}
		}
		stream.Close()
		return strings.TrimSpace(builder.String()), nil
	}
	return "", workflowv2.ErrUnsupportedContent
}

func TestingExtractDOCXText(data []byte) (string, error) {
	return extractDOCXText(data)
}

func extractPDFText(data []byte) (string, error) {
	file, err := os.CreateTemp("", "misty-agent-*.pdf")
	if err != nil {
		return "", err
	}
	path := file.Name()
	defer os.Remove(path)
	if _, err := file.Write(data); err != nil {
		file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	document, err := gxpdf.Open(path)
	if err != nil {
		return "", err
	}
	defer document.Close()
	var builder strings.Builder
	for page := 1; page <= document.PageCount(); page++ {
		text, err := document.ExtractTextFromPage(page)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		builder.WriteString("\n[Page " + strconv.Itoa(page) + "]\n")
		builder.WriteString(text)
	}
	if strings.TrimSpace(builder.String()) == "" {
		return "", workflowv2.ErrUnsupportedContent
	}
	return strings.TrimSpace(builder.String()), nil
}

func (s *SpaceLibraryService) ReadExplicitAgentAttachment(ctx context.Context, userID, spaceID, kind, resourceID string, maximumBytes int64) ([]byte, *db.LibraryDownload, error) {
	if maximumBytes < 1 || maximumBytes > 25_000_000 {
		maximumBytes = 25_000_000
	}
	var download *db.LibraryDownload
	var err error
	switch kind {
	case "library_item":
		download, err = s.database.ExplicitAgentLibraryItemDownload(ctx, userID, spaceID, resourceID)
	case "task_attachment":
		download, err = s.database.ExplicitAgentTaskAttachmentDownload(ctx, userID, spaceID, resourceID)
	case "chat_attachment":
		download, err = s.database.MessageAttachmentDownload(ctx, userID, spaceID, resourceID)
	default:
		return nil, nil, db.ErrSpaceInvalid
	}
	if err != nil {
		return nil, nil, err
	}
	if download.ByteSize > maximumBytes {
		return nil, download, db.ErrLibraryInvalid
	}
	stream, metadata, err := s.TestingStore.Open(ctx, download.ObjectKey)
	if err != nil {
		return nil, download, err
	}
	defer stream.Close()
	if metadata.ByteSize != download.ByteSize || metadata.SHA256 != download.SHA256 {
		return nil, download, db.ErrLibraryUploadMismatch
	}
	data, err := io.ReadAll(io.LimitReader(stream, maximumBytes+1))
	if err != nil || int64(len(data)) > maximumBytes {
		if err == nil {
			err = db.ErrLibraryInvalid
		}
		return nil, download, err
	}
	mimeType := strings.ToLower(strings.TrimSpace(strings.Split(download.MIMEType, ";")[0]))
	textual := strings.HasPrefix(mimeType, "text/") || mimeType == "application/json" || mimeType == "application/xml" || mimeType == "application/x-ndjson" || mimeType == "application/yaml" || mimeType == "text/csv"
	if textual {
		return data, download, nil
	}
	if mimeType == "application/vnd.openxmlformats-officedocument.wordprocessingml.document" || strings.HasSuffix(strings.ToLower(download.Filename), ".docx") {
		text, err := extractDOCXText(data)
		return []byte(text), download, err
	}
	if mimeType == "application/pdf" || strings.HasSuffix(strings.ToLower(download.Filename), ".pdf") {
		text, err := extractPDFText(data)
		return []byte(text), download, err
	}
	return nil, download, workflowv2.ErrUnsupportedContent
}

// transferPurposeEnabled gates the routes that move or finalize bytes for an
// upload that already exists.
//
// Unlike initiation, these cannot be used to obtain access: the upload row was
// created by an authorized route, carries its own note binding, and is proven
// by the upload token. So note_attachment is allowed here even though the
// generic initiation endpoint rejects it.
func (s *SpaceLibraryService) transferPurposeEnabled(purpose UploadPurpose) bool {
	if purpose == UploadPurposeNoteAttachment {
		return s.TestingNoteAssetsEnabled
	}
	if purpose == UploadPurposeDrawingAsset {
		return s.TestingDrawingAssetsEnabled
	}
	return s.TestingUploadPurposeEnabled(purpose)
}

func TestingIsJournalAssetPurpose(purpose UploadPurpose) bool {
	return purpose == UploadPurposeNoteAttachment ||
		purpose == UploadPurposeDrawingAsset
}

func NewSpaceLibraryService(database *db.Database, store LibraryObjectStore, uploadsEnabled bool, limits UploadLimits) (*SpaceLibraryService, error) {
	if database == nil || store == nil {
		return nil, errors.New("Library database and permanent object store are required")
	}
	if err := limits.TestingValidate(); err != nil {
		return nil, err
	}
	service := &SpaceLibraryService{
		database: database, TestingStore: store, TestingUploadsEnabled: uploadsEnabled, TestingUploadLimits: limits,
		egress: NewEgressGuard(EgressBudgetFromEnv()),
	}
	if err := service.TestingConfigureTransfers(TransferTTLsFromEnv()); err != nil {
		return nil, err
	}
	return service, nil
}

// WriteGeneratedTextArtifact creates a normal, ACL-protected Library item
// through the same quota, immutable-object, deduplication, and audit path as a
// user upload. It is intentionally text-only; richer generated artifacts must
// use a registered renderer/provider first.
func (s *SpaceLibraryService) WriteGeneratedTextArtifact(ctx context.Context, userID, spaceID, filename, content string, provenance map[string]any) (*db.SpaceLibraryItem, error) {
	filename = sanitizeLibraryFilename(filename)
	data := []byte(content)
	if filename == "" || len(data) == 0 || int64(len(data)) > s.TestingUploadLimits.Max(UploadPurposeLibrary) {
		return nil, db.ErrLibraryInvalid
	}
	digest := sha256.Sum256(data)
	digestHex := hex.EncodeToString(digest[:])
	token, err := security.GenerateSecureToken()
	if err != nil {
		return nil, err
	}
	tokenHash := security.HashToken(token)
	objectKey := "library/" + strings.ReplaceAll(uuid.NewString(), "-", "")
	upload, err := s.database.CreateLibraryUpload(ctx, userID, spaceID, "library", filename, "text/markdown; charset=utf-8", int64(len(data)), digestHex, objectKey, tokenHash, time.Now().Add(libraryUploadLifetime).UTC())
	if err != nil {
		return nil, err
	}
	if _, err = s.database.SetLibraryUploadState(ctx, userID, spaceID, upload.ID, tokenHash, "initiated", "uploading"); err != nil {
		return nil, err
	}
	metadata := LibraryObjectMetadata{ByteSize: int64(len(data)), SHA256: digestHex, MIMEType: "text/markdown; charset=utf-8"}
	if err = s.TestingStore.Put(ctx, objectKey, bytes.NewReader(data), metadata); err != nil {
		s.rejectAndDelete(ctx, upload, tokenHash, "invalid", "object_write_failed")
		return nil, err
	}
	if _, err = s.database.SetLibraryUploadState(ctx, userID, spaceID, upload.ID, tokenHash, "uploading", "uploaded_unverified"); err != nil {
		_ = s.TestingStore.Delete(ctx, objectKey)
		return nil, err
	}
	intrinsic, _ := json.Marshal(map[string]any{"generated": true, "provenance": provenance})
	completed, err := s.database.CompleteLibraryUpload(ctx, userID, spaceID, upload.ID, tokenHash, int64(len(data)), digestHex, metadata.MIMEType, intrinsic)
	if err != nil {
		_ = s.TestingStore.Delete(ctx, objectKey)
		return nil, err
	}
	if completed.DiscardObjectKey != "" {
		_ = s.TestingStore.Delete(ctx, completed.DiscardObjectKey)
	}
	return completed.Item, nil
}

// ReadTextItem returns bounded normalized source bytes for the universal
// workflow reader. Binary Office/PDF/image formats remain provider-specific
// and must not be misrepresented as readable text.
func (s *SpaceLibraryService) ReadTextItem(ctx context.Context, userID, spaceID, itemID string, maximumBytes int64) ([]byte, *db.LibraryDownload, error) {
	if maximumBytes < 1 || maximumBytes > 25_000_000 {
		maximumBytes = 25_000_000
	}
	download, err := s.database.LibraryItemDownload(ctx, userID, spaceID, itemID)
	if err != nil {
		return nil, nil, err
	}
	mimeType := strings.ToLower(strings.TrimSpace(strings.Split(download.MIMEType, ";")[0]))
	textual := strings.HasPrefix(mimeType, "text/") || mimeType == "application/json" || mimeType == "application/xml" || mimeType == "application/x-ndjson" || mimeType == "application/yaml"
	if !textual {
		return nil, download, workflowv2.ErrUnsupportedContent
	}
	if download.ByteSize > maximumBytes {
		return nil, download, db.ErrLibraryInvalid
	}
	reader, metadata, err := s.TestingStore.Open(ctx, download.ObjectKey)
	if err != nil {
		return nil, download, err
	}
	defer reader.Close()
	if metadata.ByteSize != download.ByteSize || metadata.SHA256 != download.SHA256 {
		return nil, download, db.ErrLibraryUploadMismatch
	}
	data, err := io.ReadAll(io.LimitReader(reader, maximumBytes+1))
	if err != nil {
		return nil, download, err
	}
	if int64(len(data)) > maximumBytes {
		return nil, download, db.ErrLibraryInvalid
	}
	return data, download, nil
}

func (s *SpaceLibraryService) Items() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		if r.Method == http.MethodGet {
			queryValues := r.URL.Query()
			sensitiveScope := ""
			if queryValues.Get("visibility") == "hidden" {
				sensitiveScope = "hidden"
			} else if queryValues.Get("collection") == "recently-deleted" {
				sensitiveScope = "recently_deleted"
			}
			if sensitiveScope != "" {
				if err := s.validateLibraryReauthentication(r, userID, spaceID, sensitiveScope); err != nil {
					writeLibraryError(w, err)
					return
				}
			}
			limit, _ := strconv.Atoi(queryValues.Get("limit"))
			if limit < 1 || limit > 200 {
				limit = 100
			}
			query := db.LibraryItemQuery{
				After:      queryValues.Get("after"),
				Limit:      limit,
				Collection: queryValues.Get("collection"),
				Search:     queryValues.Get("q"),
				Sort:       queryValues.Get("sort"),
				Direction:  queryValues.Get("direction"),
				MediaType:  queryValues.Get("media_type"),
				Utility:    queryValues.Get("utility"),
				Visibility: queryValues.Get("visibility"),
				AlbumID:    queryValues.Get("album_id"),
				Favorite:   queryValues.Get("favorite") == "true",
			}
			for value, target := range map[string]**time.Time{
				queryValues.Get("date_from"): &query.DateFrom,
				queryValues.Get("date_to"):   &query.DateTo,
			} {
				if value == "" {
					continue
				}
				parsed, parseErr := time.Parse(time.RFC3339, value)
				if parseErr != nil {
					writeLibraryError(w, db.ErrLibraryInvalid)
					return
				}
				*target = &parsed
			}
			items, err := s.database.LibraryItems(r.Context(), userID, spaceID, query)
			if err != nil {
				writeLibraryError(w, err)
				return
			}
			nextAfter := ""
			if len(items) == query.Limit {
				nextAfter = items[len(items)-1].ID
			}
			writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_after": nextAfter})
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *SpaceLibraryService) Reauthenticate() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			Password string `json:"password"`
			Scope    string `json:"scope"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		valid, err := s.database.VerifyUserPassword(r.Context(), userID, body.Password)
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		if !valid {
			_ = s.database.RecordLibraryReauthenticationDenied(r.Context(), userID, chi.URLParam(r, "spaceID"), body.Scope)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "reauthentication_failed"})
			return
		}
		token, err := security.GenerateSecureToken()
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		expiresAt, err := s.database.CreateLibraryReauthenticationGrant(r.Context(), userID, chi.URLParam(r, "spaceID"), body.Scope, security.HashToken(token), 5*time.Minute)
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"token": token, "scope": body.Scope, "expires_at": expiresAt})
	}
}

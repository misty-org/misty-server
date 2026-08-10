package api

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"path/filepath"
	"strings"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func libraryRenditionIntrinsicMetadata(source db.LibraryTransferItem) json.RawMessage {
	metadata := map[string]any{}
	_ = json.Unmarshal(source.IntrinsicMetadata, &metadata)
	metadata["byte_size"] = source.ByteSize
	metadata["server_detected_mime_type"] = source.MIMEType
	metadata["edited_from_item_id"] = source.ItemID
	var definition db.LibraryEditDefinition
	if json.Unmarshal(source.RenditionDefinition, &definition) == nil {
		width, widthOK := metadataNumber(metadata["width"])
		height, heightOK := metadataNumber(metadata["height"])
		if widthOK && heightOK {
			if definition.Crop != nil {
				width *= definition.Crop.Width
				height *= definition.Crop.Height
			}
			if definition.Rotation == 90 || definition.Rotation == 270 {
				width, height = height, width
			}
			metadata["width"], metadata["height"] = int64(math.Round(width)), int64(math.Round(height))
		}
		if definition.Trim != nil {
			metadata["duration"] = definition.Trim.End - definition.Trim.Start
		}
		metadata["edit_definition"] = definition
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}

func metadataNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func (s *SpaceLibraryService) rejectAndDelete(ctx context.Context, upload *db.LibraryUpload, tokenHash, state, code string) {
	_ = s.database.RejectLibraryUpload(ctx, upload.UserID, upload.SpaceID, upload.ID, tokenHash, state, code)
	_ = s.TestingStore.Delete(ctx, upload.ObjectKey)
}

var TestingErrLibraryMalware = errors.New("malware detected")

type libraryInspectionError struct{ code string }

func (e libraryInspectionError) Error() string { return e.code }

func TestingLibraryInspectionCode(err error) string {
	if errors.Is(err, TestingErrLibraryMalware) {
		return "malware_detected"
	}
	var typed libraryInspectionError
	if errors.As(err, &typed) {
		return typed.code
	}
	return "content_rejected"
}

func TestingInspectLibraryContent(reader io.Reader, byteSize int64, filename, declaredMIME string) (string, map[string]any, error) {
	if byteSize < 1 || byteSize > db.MaxSpaceStorageBytes {
		return "", nil, libraryInspectionError{code: "invalid_size"}
	}
	extension := strings.ToLower(filepath.Ext(filename))
	blockedExtensions := map[string]bool{".exe": true, ".dll": true, ".dylib": true, ".so": true, ".sh": true, ".bash": true, ".zsh": true, ".bat": true, ".cmd": true, ".ps1": true, ".js": true, ".mjs": true, ".html": true, ".htm": true, ".svg": true, ".jar": true, ".app": true, ".dmg": true, ".pkg": true, ".iso": true, ".zip": true, ".rar": true, ".7z": true, ".gz": true, ".tar": true}
	if blockedExtensions[extension] {
		return "", nil, libraryInspectionError{code: "dangerous_file_type"}
	}
	hasher := sha256.New()
	buffered := bufio.NewReaderSize(io.LimitReader(reader, byteSize+1), 64*1024)
	first := make([]byte, 512)
	n, firstErr := io.ReadFull(buffered, first)
	if firstErr != nil && !errors.Is(firstErr, io.ErrUnexpectedEOF) && !errors.Is(firstErr, io.EOF) {
		return "", nil, firstErr
	}
	first = first[:n]
	_, _ = hasher.Write(first)
	detected := http.DetectContentType(first)
	blockedMIMEs := []string{"text/html", "image/svg+xml", "application/x-msdownload", "application/x-sh", "application/javascript", "text/javascript"}
	for _, blocked := range blockedMIMEs {
		if strings.EqualFold(strings.TrimSpace(strings.Split(detected, ";")[0]), blocked) {
			return "", nil, libraryInspectionError{code: "dangerous_file_type"}
		}
	}
	eicar := []byte("EICAR-STANDARD-ANTIVIRUS-TEST-FILE")
	tail := append([]byte(nil), first...)
	foundEICAR := bytesContains(tail, eicar)
	readBytes := int64(len(first))
	chunk := make([]byte, 64*1024)
	for {
		n, err := buffered.Read(chunk)
		if n > 0 {
			readBytes += int64(n)
			_, _ = hasher.Write(chunk[:n])
			window := append(tail, chunk[:n]...)
			if bytesContains(window, eicar) {
				foundEICAR = true
			}
			keep := len(eicar) - 1
			if len(window) > keep {
				tail = append(tail[:0], window[len(window)-keep:]...)
			} else {
				tail = append(tail[:0], window...)
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", nil, err
		}
	}
	if readBytes != byteSize {
		return "", nil, libraryInspectionError{code: "verification_mismatch"}
	}
	if foundEICAR {
		return "", nil, TestingErrLibraryMalware
	}
	return detected, map[string]any{
		"sha256":                    hex.EncodeToString(hasher.Sum(nil)),
		"byte_size":                 byteSize,
		"server_detected_mime_type": detected,
		"client_declared_mime_type": strings.TrimSpace(declaredMIME),
	}, nil
}

func bytesContains(data, pattern []byte) bool {
	return strings.Contains(string(data), string(pattern))
}

func sanitizeLibraryFilename(value string) string {
	value = strings.TrimSpace(filepath.Base(strings.ReplaceAll(value, "\\", "/")))
	value = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 || r == '/' || r == '\\' {
			return -1
		}
		return r
	}, value)
	if len([]rune(value)) > 255 {
		value = string([]rune(value)[:255])
	}
	return value
}

func writeLibraryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrLibraryNotFound), errors.Is(err, ErrLibraryObjectNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
	case errors.Is(err, db.ErrLibraryForbidden):
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "forbidden"})
	case errors.Is(err, db.ErrLibraryReauthentication):
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "library_reauthentication_required"})
	case errors.Is(err, db.ErrLibraryInvalid):
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_request"})
	case errors.Is(err, db.ErrLibraryQuota):
		writeJSON(w, http.StatusConflict, map[string]any{"code": "owner_storage_quota_exceeded", "owner_can_upgrade": true})
	case errors.Is(err, db.ErrLibraryConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "version_conflict"})
	case errors.Is(err, db.ErrLibraryUploadMismatch):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "upload_verification_failed"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "internal_error"})
	}
}

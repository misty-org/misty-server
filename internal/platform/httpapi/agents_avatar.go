package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

const maxAgentAvatarBytes = 5 << 20

type uploadedAgentAvatar struct {
	Kind    string `json:"kind"`
	AssetID string `json:"asset_id"`
	Version int64  `json:"version"`
}

func agentAvatarObjectKey(assetID string) string { return "avatars/" + assetID }

func (s *AgentsService) PersonalAgentAvatar() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		agentID := strings.TrimSpace(chi.URLParam(r, "agentID"))
		switch r.Method {
		case http.MethodGet:
			version, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("version")), 10, 64)
			if err != nil || version < 1 {
				writeAgentError(w, db.ErrSpaceInvalid)
				return
			}
			avatar, err := s.database.PersonalAgentAvatarForUser(r.Context(), userID, agentID, version)
			if err != nil {
				writeAgentError(w, err)
				return
			}
			var uploaded uploadedAgentAvatar
			if json.Unmarshal(avatar, &uploaded) != nil || uploaded.Kind != "upload" || uploaded.AssetID == "" || uploaded.Version != version {
				writeAgentError(w, db.ErrPersonalAgentNotFound)
				return
			}
			s.servePersonalAgentAvatar(w, r, uploaded)
		case http.MethodPut:
			if s.avatarStore == nil {
				http.Error(w, "avatar storage unavailable", http.StatusServiceUnavailable)
				return
			}
			current, err := s.database.PersonalAgentByID(r.Context(), userID, agentID)
			if err != nil {
				writeAgentError(w, err)
				return
			}
			data, mimeType, ok := readAgentAvatar(w, r)
			if !ok {
				return
			}
			nextVersion := current.Version + 1
			assetID := "agent-avatar_" + agentID + "_v" + strconv.FormatInt(nextVersion, 10)
			sum := sha256.Sum256(data)
			if err := s.avatarStore.Put(r.Context(), agentAvatarObjectKey(assetID), bytes.NewReader(data), LibraryObjectMetadata{
				ByteSize: int64(len(data)), SHA256: hex.EncodeToString(sum[:]), MIMEType: mimeType,
			}); err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			current.Avatar, _ = json.Marshal(uploadedAgentAvatar{Kind: "upload", AssetID: assetID, Version: nextVersion})
			updated, err := s.database.UpdatePersonalAgent(r.Context(), userID, *current)
			if err != nil {
				_ = s.avatarStore.Delete(r.Context(), agentAvatarObjectKey(assetID))
				writeAgentError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, updated)
		default:
			w.Header().Set("Allow", "GET, PUT")
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func readAgentAvatar(w http.ResponseWriter, r *http.Request) ([]byte, string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxAgentAvatarBytes+1)
	data, err := io.ReadAll(r.Body)
	if err != nil || len(data) == 0 || len(data) > maxAgentAvatarBytes {
		http.Error(w, "Agent avatar must be 5 MB or smaller", http.StatusRequestEntityTooLarge)
		return nil, "", false
	}
	mimeType := http.DetectContentType(data)
	switch mimeType {
	case "image/png", "image/jpeg":
		config, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 4096 || config.Height > 4096 {
			http.Error(w, "valid PNG or JPEG required", http.StatusBadRequest)
			return nil, "", false
		}
	case "image/webp":
		width, height, valid := webPDimensions(data)
		if !valid || width < 1 || height < 1 || width > 4096 || height > 4096 {
			http.Error(w, "valid WebP required", http.StatusBadRequest)
			return nil, "", false
		}
	default:
		http.Error(w, "PNG, JPEG, or WebP required", http.StatusUnsupportedMediaType)
		return nil, "", false
	}
	return data, mimeType, true
}

func TestingReadAgentAvatar(w http.ResponseWriter, r *http.Request) ([]byte, string, bool) {
	return readAgentAvatar(w, r)
}

func webPDimensions(data []byte) (int, int, bool) {
	if len(data) < 30 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return 0, 0, false
	}
	switch string(data[12:16]) {
	case "VP8X":
		width := 1 + int(data[24]) + int(data[25])<<8 + int(data[26])<<16
		height := 1 + int(data[27]) + int(data[28])<<8 + int(data[29])<<16
		return width, height, true
	case "VP8 ":
		if len(data) < 30 || data[23] != 0x9d || data[24] != 0x01 || data[25] != 0x2a {
			return 0, 0, false
		}
		return int(binary.LittleEndian.Uint16(data[26:28]) & 0x3fff), int(binary.LittleEndian.Uint16(data[28:30]) & 0x3fff), true
	case "VP8L":
		if len(data) < 25 || data[20] != 0x2f {
			return 0, 0, false
		}
		bits := binary.LittleEndian.Uint32(data[21:25])
		return int(bits&0x3fff) + 1, int((bits>>14)&0x3fff) + 1, true
	default:
		return 0, 0, false
	}
}

func (s *AgentsService) servePersonalAgentAvatar(w http.ResponseWriter, r *http.Request, avatar uploadedAgentAvatar) {
	if s.avatarStore == nil {
		http.Error(w, "avatar not found", http.StatusNotFound)
		return
	}
	reader, metadata, err := s.avatarStore.Open(r.Context(), agentAvatarObjectKey(avatar.AssetID))
	if err != nil {
		if errors.Is(err, ErrLibraryObjectNotFound) {
			http.Error(w, "avatar not found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer reader.Close()
	w.Header().Set("Content-Type", metadata.MIMEType)
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("ETag", `"agent-avatar-`+avatar.AssetID+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, reader)
}

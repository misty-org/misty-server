package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

const maxSelfHostCollaborationStateBytes = 8 * 1024 * 1024

var collaborationResourceIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,200}$`)

func SelfHostCollaborationState(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if InstanceConfigFromEnv().Deployment != "self_hosted" || !validInternalCollaborationSecret(r) {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
			return
		}
		resourceType := chi.URLParam(r, "resourceType")
		resourceID := chi.URLParam(r, "resourceID")
		if (resourceType != "note" && resourceType != "drawing") || !collaborationResourceIDPattern.MatchString(resourceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_resource"})
			return
		}
		switch r.Method {
		case http.MethodGet:
			state, checksum, aclVersion, err := database.SelfHostCollaborationState(r.Context(), resourceType, resourceID)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "internal_error"})
				return
			}
			if state == nil {
				writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
				return
			}
			w.Header().Set("Content-Type", "application/vnd.yjs.update")
			w.Header().Set("X-Content-SHA256", checksum)
			w.Header().Set("X-Misty-ACL-Version", strconv.FormatInt(aclVersion, 10))
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(state)
		case http.MethodPut:
			r.Body = http.MaxBytesReader(w, r.Body, maxSelfHostCollaborationStateBytes+1)
			state, err := io.ReadAll(r.Body)
			if err != nil || len(state) == 0 || len(state) > maxSelfHostCollaborationStateBytes {
				writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"code": "document_too_large"})
				return
			}
			digest := sha256.Sum256(state)
			checksum := hex.EncodeToString(digest[:])
			if provided := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Content-SHA256"))); provided != "" && !hmac.Equal([]byte(provided), []byte(checksum)) {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "checksum_mismatch"})
				return
			}
			aclVersion, err := strconv.ParseInt(strings.TrimSpace(r.Header.Get("X-Misty-ACL-Version")), 10, 64)
			if err != nil || aclVersion < 0 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_acl_version"})
				return
			}
			if err := database.PutSelfHostCollaborationState(r.Context(), resourceType, resourceID, state, checksum, aclVersion); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "internal_error"})
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case http.MethodDelete:
			if err := database.DeleteSelfHostCollaborationState(r.Context(), resourceType, resourceID); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "internal_error"})
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.Header().Set("Allow", "GET, PUT, DELETE")
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"code": "method_not_allowed"})
		}
	}
}

func validInternalCollaborationSecret(r *http.Request) bool {
	expected := strings.TrimSpace(envconfig.Getenv("MISTY_COLLAB_INTERNAL_SECRET"))
	provided := strings.TrimSpace(r.Header.Get("X-Misty-Internal-Secret"))
	return len(expected) >= 32 && hmac.Equal([]byte(expected), []byte(provided))
}

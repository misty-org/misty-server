package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func (s *SpacesService) InboxClear() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			Tab string `json:"tab"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		if body.Tab != "mentions" {
			body.Tab = "unreads"
		}
		if err := s.database.ClearSpaceInbox(r.Context(), userID, body.Tab); err != nil {
			writeSpaceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *SpacesService) StudioResources(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		if r.Method == http.MethodGet {
			items, err := s.database.SpaceStudioResources(r.Context(), userID, spaceID, kind)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"resources": items})
			return
		}
		var item db.SpaceStudioResource
		if decodeJSON(w, r, &item) != nil {
			return
		}
		item.SpaceID, item.Kind = spaceID, kind
		saved, err := s.database.SaveSpaceStudioResource(r.Context(), userID, item)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, saved)
	}
}

func (s *SpacesService) DeleteStudioResource(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if err := s.database.DeleteSpaceStudioResource(r.Context(), userID, chi.URLParam(r, "spaceID"), kind, chi.URLParam(r, "resourceID")); err != nil {
			writeSpaceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *SpacesService) RunStudioResource(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, resourceID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "resourceID")
		var body struct {
			Prompt       string          `json:"prompt"`
			CapabilityID string          `json:"capability_id"`
			Input        json.RawMessage `json:"input"`
		}
		if r.ContentLength > 0 && decodeJSON(w, r, &body) != nil {
			return
		}
		input := body.Input
		if len(input) == 0 {
			input, _ = json.Marshal(map[string]string{"prompt": strings.TrimSpace(body.Prompt)})
		}
		run, err := s.database.CreateSpaceRun(r.Context(), userID, spaceID, kind, resourceID, "test", body.CapabilityID, input)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if run.State == "awaiting_approval" {
			writeJSON(w, http.StatusAccepted, run)
			return
		}
		finished, err := s.executeCanonicalAgentRun(r, run, body.Prompt)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, finished)
	}
}

func TestingIsPublicWorkflowIP(ip net.IP) bool {
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return false
	}
	for _, cidr := range []string{"100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "2001:db8::/32"} {
		_, network, _ := net.ParseCIDR(cidr)
		if network.Contains(ip) {
			return false
		}
	}
	return true
}

// SpaceTargetFingerprint is used in audit logs and tests when a stable, safe
// identifier is needed. It never returns the Drive target itself.
func SpaceTargetFingerprint(target string) string {
	sum := sha256.Sum256([]byte(target))
	return hex.EncodeToString(sum[:8])
}

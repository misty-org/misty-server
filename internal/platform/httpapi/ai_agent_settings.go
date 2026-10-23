package api

import (
	"net/http"
	"strings"

	"github.com/kannachi323/misty/server/internal/platform/db"
)

// normalizeAgentSite turns "https://www.Example.com/path" into "example.com",
// the host agents may not act on (with its subdomains). Devices apply the same
// rule (src-tauri/src/infra/browser/page_agent/site_blocks.rs).
func normalizeAgentSite(site string) (string, bool) {
	host := strings.ToLower(strings.TrimSpace(site))
	host = strings.TrimPrefix(strings.TrimPrefix(host, "https://"), "http://")
	if cut := strings.IndexAny(host, "/?#"); cut >= 0 {
		host = host[:cut]
	}
	host = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(host, "*."), "www."), ".")
	if host == "" || len(host) > 253 || !strings.Contains(host, ".") {
		return "", false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 {
			return "", false
		}
		for _, char := range label {
			if !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-') {
				return "", false
			}
		}
	}
	return host, true
}

// AgentSitesSetting changes which sites agents may read but not act on.
func (s *AIService) AgentSitesSetting() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		var body struct {
			BlockSensitive *bool     `json:"block_sensitive"`
			BlockedSites   *[]string `json:"blocked_sites"`
		}
		if decodeAIJSON(w, r, &body) != nil {
			return
		}
		if body.BlockSensitive == nil && body.BlockedSites == nil {
			WriteAIError(w, db.ErrSpaceInvalid)
			return
		}
		var sites []string
		if body.BlockedSites != nil {
			sites = []string{}
			seen := map[string]bool{}
			for _, raw := range *body.BlockedSites {
				site, valid := normalizeAgentSite(raw)
				if !valid {
					writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_site", "message": "“" + truncateAgentRuntimeText(strings.TrimSpace(raw), 80) + "” isn’t a site address. Use a form like example.com."})
					return
				}
				if !seen[site] {
					seen[site] = true
					sites = append(sites, site)
				}
			}
			if len(sites) > 200 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"code": "too_many_sites", "message": "Block at most 200 sites."})
				return
			}
		}
		if err := s.database.SetAIAgentSiteBlocks(r.Context(), userID, body.BlockSensitive, sites); err != nil {
			WriteAIError(w, err)
			return
		}
		settings, _, err := s.database.AISettings(r.Context(), userID)
		if err != nil {
			WriteAIError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"settings": settings})
	}
}

// KeepAwakeSetting changes whether a computer stays awake while an agent works
// on it (docs/sync/devices.md).
func (s *AIService) KeepAwakeSetting() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		var body struct {
			KeepAwake *bool `json:"keep_awake_for_agents"`
		}
		if decodeAIJSON(w, r, &body) != nil {
			return
		}
		if body.KeepAwake == nil {
			WriteAIError(w, db.ErrSpaceInvalid)
			return
		}
		if err := s.database.SetAIKeepAwake(r.Context(), userID, *body.KeepAwake); err != nil {
			WriteAIError(w, err)
			return
		}
		settings, _, err := s.database.AISettings(r.Context(), userID)
		if err != nil {
			WriteAIError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"settings": settings})
	}
}

// ScreenLocationSetting changes only where agents open a new screen when a task
// needs one: a new tab in the user's Misty window, or ask.
func (s *AIService) ScreenLocationSetting() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		var body struct {
			Location string `json:"location"`
		}
		if decodeAIJSON(w, r, &body) != nil {
			return
		}
		if err := s.database.SetAIScreenLocation(r.Context(), userID, body.Location); err != nil {
			WriteAIError(w, err)
			return
		}
		settings, _, err := s.database.AISettings(r.Context(), userID)
		if err != nil {
			WriteAIError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"settings": settings})
	}
}

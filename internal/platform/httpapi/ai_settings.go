package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func (s *AIService) Settings() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		switch r.Method {
		case http.MethodGet:
			settings, preferences, err := s.database.AISettings(r.Context(), userID)
			if err != nil {
				TestingWriteAIError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"settings": settings, "preferences": preferences})
		case http.MethodPut:
			var body struct {
				Enabled                bool  `json:"enabled"`
				RetentionDays          int   `json:"retention_days"`
				CursorCompanionEnabled *bool `json:"cursor_companion_enabled"`
				MemoryEnabled          *bool `json:"memory_enabled"`
			}
			if decodeAIJSON(w, r, &body) != nil {
				return
			}
			cursorCompanionEnabled := true
			memoryEnabled := true
			current, _, currentErr := s.database.AISettings(r.Context(), userID)
			if body.CursorCompanionEnabled != nil {
				cursorCompanionEnabled = *body.CursorCompanionEnabled
			} else if currentErr == nil {
				cursorCompanionEnabled = current.CursorCompanionEnabled
			}
			if body.MemoryEnabled != nil {
				memoryEnabled = *body.MemoryEnabled
			} else if currentErr == nil {
				memoryEnabled = current.MemoryEnabled
			}
			settings, err := s.database.UpdateAISettings(r.Context(), userID, body.Enabled, body.RetentionDays, cursorCompanionEnabled, memoryEnabled)
			if err != nil {
				TestingWriteAIError(w, err)
				return
			}
			if !settings.Enabled {
				s.invocations.cancelAllForUser(userID)
			}
			writeJSON(w, http.StatusOK, map[string]any{"settings": settings})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

// AppActionsSetting changes only whether agents ask before acting for the
// person (ask) and before paying (payments).
func (s *AIService) AppActionsSetting() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		var body struct {
			Ask      *bool `json:"ask"`
			Payments *bool `json:"payments"`
		}
		if decodeAIJSON(w, r, &body) != nil {
			return
		}
		if body.Ask == nil && body.Payments == nil {
			TestingWriteAIError(w, db.ErrSpaceInvalid)
			return
		}
		if err := s.database.SetAIAppActionsAsk(r.Context(), userID, body.Ask, body.Payments); err != nil {
			TestingWriteAIError(w, err)
			return
		}
		settings, _, err := s.database.AISettings(r.Context(), userID)
		if err != nil {
			TestingWriteAIError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"settings": settings})
	}
}

// normalizeAgentSite turns "https://www.Example.com/path" into "example.com",
// the host agents may not act on (with its subdomains). Devices apply the same
// rule (src-tauri/src/infra/browser_page/site_blocks.rs).
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
			TestingWriteAIError(w, db.ErrSpaceInvalid)
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
			TestingWriteAIError(w, err)
			return
		}
		settings, _, err := s.database.AISettings(r.Context(), userID)
		if err != nil {
			TestingWriteAIError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"settings": settings})
	}
}

// KeepAwakeSetting changes whether a computer stays awake while an agent works
// on it (docs/design/agents-on-devices/BRIEF.md).
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
			TestingWriteAIError(w, db.ErrSpaceInvalid)
			return
		}
		if err := s.database.SetAIKeepAwake(r.Context(), userID, *body.KeepAwake); err != nil {
			TestingWriteAIError(w, err)
			return
		}
		settings, _, err := s.database.AISettings(r.Context(), userID)
		if err != nil {
			TestingWriteAIError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"settings": settings})
	}
}

// ScreenLocationSetting changes only where agents open a new screen when a task
// needs one: a new tab in the user's Misty window, a separate window, or ask.
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
			TestingWriteAIError(w, err)
			return
		}
		settings, _, err := s.database.AISettings(r.Context(), userID)
		if err != nil {
			TestingWriteAIError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"settings": settings})
	}
}

func (s *AIService) SurfacePreference() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		if r.Method != http.MethodPut {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Proactive    bool            `json:"proactive_enabled"`
			SavedActions json.RawMessage `json:"saved_actions"`
		}
		if decodeAIJSON(w, r, &body) != nil {
			return
		}
		surfaceID := strings.TrimSpace(chi.URLParam(r, "surfaceID"))
		if !aiSurfaceIDs[surfaceID] || !validAISavedActions(body.SavedActions) {
			http.Error(w, "invalid surface preference", http.StatusBadRequest)
			return
		}
		preference, err := s.database.UpsertAISurfacePreference(r.Context(), userID, db.AISurfacePreference{
			SurfaceID: surfaceID,
			Proactive: body.Proactive, SavedActions: body.SavedActions,
		})
		if err != nil {
			TestingWriteAIError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"preference": preference})
	}
}

func validAISavedActions(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	if len(raw) > 64<<10 {
		return false
	}
	var actions []struct {
		ID                    string `json:"id"`
		Label                 string `json:"label"`
		Prompt                string `json:"prompt"`
		RequestedArtifactKind string `json:"requested_artifact_kind,omitempty"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&actions) != nil || len(actions) > 20 {
		return false
	}
	seen := map[string]bool{}
	for _, action := range actions {
		action.ID, action.Label, action.Prompt = strings.TrimSpace(action.ID), strings.TrimSpace(action.Label), strings.TrimSpace(action.Prompt)
		if action.ID == "" || seen[action.ID] || len(action.ID) > 100 || action.Label == "" || len([]rune(action.Label)) > 80 || action.Prompt == "" || len(action.Prompt) > 8<<10 {
			return false
		}
		seen[action.ID] = true
		if action.RequestedArtifactKind != "" && action.RequestedArtifactKind != "text_patch" {
			if _, ok := aiArtifactSpecs[action.RequestedArtifactKind]; !ok {
				return false
			}
		}
	}
	return true
}

func (s *AIService) InvocationFeedback() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		var body struct {
			Rating  int    `json:"rating"`
			Reason  string `json:"reason"`
			Comment string `json:"comment"`
		}
		if decodeAIJSON(w, r, &body) != nil {
			return
		}
		if err := s.database.RecordAIFeedback(r.Context(), userID, chi.URLParam(r, "invocationID"), body.Rating, body.Reason, body.Comment); err != nil {
			TestingWriteAIError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

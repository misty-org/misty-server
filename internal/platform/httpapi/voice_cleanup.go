package api

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/modelruntime"
)

// VoiceCleanup tidies one dictation with the fast model before the desktop
// inserts it. Turning AI cleanup off in Settings skips this call entirely.
func (s *AIService) VoiceCleanup() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		var body struct {
			Text       string   `json:"text"`
			Style      string   `json:"style"`
			Dictionary []string `json:"dictionary"`
			Language   string   `json:"language"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
		if decodeAIJSON(w, r, &body) != nil || utf8.RuneCountInString(body.Text) > 6000 || len(body.Dictionary) > 200 || len(body.Language) > 16 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_cleanup"})
			return
		}
		dictionary := make([]string, 0, len(body.Dictionary))
		for _, term := range body.Dictionary {
			if term = strings.TrimSpace(term); term != "" && len(term) <= 80 {
				dictionary = append(dictionary, term)
			}
		}
		text, err := s.runtime.CleanDictation(r.Context(), user, defaultRoleModel("dictation-cleanup"), agent.DictationCleanup{Text: body.Text, Style: body.Style, Dictionary: dictionary, Language: body.Language})
		if err != nil && writeModelError(w, err) {
			return
		}
		switch {
		case errors.Is(err, modelruntime.ErrUnavailable):
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "cleanup_unavailable", "message": "Cleanup isn't available right now. Your words were typed as heard."})
		case err != nil:
			writeJSON(w, http.StatusBadGateway, map[string]string{"code": "cleanup_failed", "message": "Cleanup didn't finish. Your words were typed as heard."})
		default:
			writeJSON(w, http.StatusOK, map[string]string{"text": text})
		}
	}
}

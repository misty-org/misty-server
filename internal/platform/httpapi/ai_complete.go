package api

import (
	"net/http"
	"strconv"
	"time"
)

// Legacy unscoped completion cannot bypass Misty's Space admission.
func (s *AIService) Complete() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.requireUser(w, r); !ok {
			return
		}
		writeJSON(w, http.StatusGone, map[string]string{"code": "misty_handoff_required", "message": "Start a Space-scoped Misty invocation through /ai/invocations."})
	}
}

func TestingWriteAIRateLimit(w http.ResponseWriter, retryAfter time.Duration) {
	seconds := retryAfterSeconds(retryAfter)
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	writeJSON(w, http.StatusTooManyRequests, map[string]any{
		"code": "rate_limited", "message": "Agent request limit reached. Try again later.",
		"retry_after_seconds": seconds,
	})
}

func (s *AIService) requireUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID, err := sessionUserID(r, s.database)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return "", false
	}
	if userID == "" {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return "", false
	}
	return userID, true
}

func decodeAIJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	return DecodeAIJSONWithLimit(w, r, dst, MaxAIJSONBodyBytes)
}

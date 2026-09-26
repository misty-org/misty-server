package api

import "net/http"

func (s *AIService) MistyActivity() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		spaceID := ""
		rows, err := s.database.MistyActivity(r.Context(), userID, spaceID, r.URL.Query().Get("agent_id"))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"entries": rows})
	}
}

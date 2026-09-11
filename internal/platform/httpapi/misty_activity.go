package api

import "net/http"

func (s *AIService) MistyActivity() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		spaceID := r.URL.Query().Get("space_id")
		if spaceID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"message": "Select a Space."})
			return
		}
		rows, err := s.database.MistyActivity(r.Context(), userID, spaceID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"entries": rows})
	}
}

package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// NotionSearch searches only the indexed records beneath selected sources.
func (s *SpacesService) NotionSearch() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		query := strings.TrimSpace(r.URL.Query().Get("q"))
		records, err := s.database.ProviderContentRecords(
			r.Context(), userID, spaceID, "notion", query, 50,
		)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		pages := []json.RawMessage{}
		for _, record := range records {
			if record.RecordType != "page" {
				continue
			}
			var content struct {
				Object json.RawMessage `json:"object"`
			}
			if json.Unmarshal(record.Content, &content) == nil && len(content.Object) > 0 {
				pages = append(pages, content.Object)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"pages": pages})
	}
}

// proxyNotion performs the call and relays Notion's JSON verbatim, so the
// client sees the real object rather than a lossy re-encoding.
func (s *SpacesService) proxyNotion(
	w http.ResponseWriter,
	r *http.Request,
	resource db.ProviderSharedResource,
	method, endpoint string,
	body any,
) {
	raw, err := s.notionRequest(r.Context(), resource, method, endpoint, body)
	if err != nil {
		writeProviderFailure(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

func notionParentID(raw json.RawMessage) string {
	var parent map[string]any
	if json.Unmarshal(raw, &parent) != nil {
		return ""
	}
	for _, key := range []string{"database_id", "data_source_id", "page_id"} {
		if value, _ := parent[key].(string); strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

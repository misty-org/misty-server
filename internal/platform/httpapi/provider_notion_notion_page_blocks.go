package api

import (
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// NotionPageBlocks reads a page's block children, paginating to a bounded
// depth so one enormous page cannot stall the request.
func (s *SpacesService) NotionPageBlocks() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, pageID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "pageID")
		resource, err := s.notionResource(r.Context(), userID, spaceID, pageID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		blocks := []json.RawMessage{}
		cursor := ""
		for pages := 0; pages < 20; pages++ {
			endpoint := "https://api.notion.com/v1/blocks/" + url.PathEscape(pageID) + "/children?page_size=100"
			if cursor != "" {
				endpoint += "&start_cursor=" + url.QueryEscape(cursor)
			}
			raw, err := s.notionRequest(r.Context(), *resource, http.MethodGet, endpoint, nil)
			if err != nil {
				writeProviderFailure(w, err)
				return
			}
			var page struct {
				Results    []json.RawMessage `json:"results"`
				NextCursor string            `json:"next_cursor"`
				HasMore    bool              `json:"has_more"`
			}
			if json.Unmarshal(raw, &page) != nil {
				writeJSON(w, http.StatusBadGateway, map[string]string{"code": "invalid_response"})
				return
			}
			blocks = append(blocks, page.Results...)
			if !page.HasMore || page.NextCursor == "" {
				break
			}
			cursor = page.NextCursor
		}
		writeJSON(w, http.StatusOK, map[string]any{"blocks": blocks})
	}
}

// NotionPages creates a page. This is a real outward write, so it is only
// reachable from an explicit user action in the client.
func (s *SpacesService) NotionPages() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		var body struct {
			Parent     json.RawMessage            `json:"parent"`
			Properties map[string]json.RawMessage `json:"properties"`
			Children   []json.RawMessage          `json:"children"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		if len(body.Parent) == 0 || len(body.Properties) == 0 {
			writeSpaceError(w, db.ErrSpaceInvalid)
			return
		}
		if len(body.Children) > notionChildLimit {
			writeSpaceError(w, db.ErrSpaceInvalid)
			return
		}
		parentID := notionParentID(body.Parent)
		resource, err := s.notionResource(r.Context(), userID, spaceID, parentID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		payload := map[string]any{"parent": body.Parent, "properties": body.Properties}
		if len(body.Children) > 0 {
			payload["children"] = body.Children
		}
		raw, err := s.notionRequest(
			r.Context(), *resource, http.MethodPost, "https://api.notion.com/v1/pages", payload,
		)
		if err != nil {
			writeProviderFailure(w, err)
			return
		}
		var page struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &page) == nil && page.ID != "" {
			var object map[string]any
			_ = json.Unmarshal(raw, &object)
			content, _ := json.Marshal(map[string]any{"object": json.RawMessage(raw)})
			_ = s.database.UpsertProviderContentRecord(r.Context(), db.ProviderContentRecord{
				SpaceID: resource.SpaceID, SharedResourceID: resource.ID, Provider: "notion",
				ExternalRecordID: page.ID, ParentExternalID: parentID, RecordType: "page",
				Fingerprint: providerPayloadFingerprint(raw), DisplayName: notionObjectTitle(object),
				MIMEType: "application/vnd.notion+json", Content: content,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
	}
}

// Notion refuses an append larger than this, so the server rejects it before
// the request leaves Misty rather than surfacing an opaque 400.
const notionChildLimit = 100

// NotionBlockChildren appends blocks to an existing page.
func (s *SpacesService) NotionBlockChildren() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if r.Method != http.MethodPatch {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		spaceID, blockID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "blockID")
		resource, err := s.notionResource(r.Context(), userID, spaceID, blockID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		var body struct {
			Children []json.RawMessage `json:"children"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		if len(body.Children) == 0 || len(body.Children) > notionChildLimit {
			writeSpaceError(w, db.ErrSpaceInvalid)
			return
		}
		endpoint := "https://api.notion.com/v1/blocks/" + url.PathEscape(blockID) + "/children"
		s.proxyNotion(w, r, *resource, http.MethodPatch, endpoint, map[string]any{"children": body.Children})
	}
}

// NotionDatabase reads a database's schema, which is what lets Misty refuse to
// write a property type it cannot express.
func (s *SpacesService) NotionDatabase() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, databaseID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "databaseID")
		resource, err := s.notionResource(r.Context(), userID, spaceID, databaseID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		s.proxyNotion(w, r, *resource, http.MethodGet,
			"https://api.notion.com/v1/databases/"+url.PathEscape(databaseID), nil)
	}
}

// NotionDatabaseQuery lists a database's rows as pages.
func (s *SpacesService) NotionDatabaseQuery() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		spaceID, databaseID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "databaseID")
		resource, err := s.notionResource(r.Context(), userID, spaceID, databaseID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		raw, err := s.notionRequest(r.Context(), *resource, http.MethodPost,
			"https://api.notion.com/v1/databases/"+url.PathEscape(databaseID)+"/query",
			map[string]any{"page_size": 100})
		if err != nil {
			writeProviderFailure(w, err)
			return
		}
		var page struct {
			Results []json.RawMessage `json:"results"`
		}
		if json.Unmarshal(raw, &page) != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"code": "invalid_response"})
			return
		}
		for _, row := range page.Results {
			var value struct {
				ID             string `json:"id"`
				LastEditedTime string `json:"last_edited_time"`
			}
			if json.Unmarshal(row, &value) != nil || value.ID == "" {
				continue
			}
			var object map[string]any
			_ = json.Unmarshal(row, &object)
			content, _ := json.Marshal(map[string]any{"object": json.RawMessage(row)})
			_ = s.database.UpsertProviderContentRecord(r.Context(), db.ProviderContentRecord{
				SpaceID: resource.SpaceID, SharedResourceID: resource.ID, Provider: "notion",
				ExternalRecordID: value.ID, ParentExternalID: databaseID, RecordType: "page",
				Fingerprint: providerPayloadFingerprint(row), DisplayName: notionObjectTitle(object),
				MIMEType: "application/vnd.notion+json", Content: content,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"pages": page.Results})
	}
}

package api

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type globalSearchHit struct {
	ID        string   `json:"id"`
	AccountID string   `json:"accountId"`
	Kind      string   `json:"kind"`
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	Keywords  []string `json:"keywords"`
	Href      string   `json:"href"`
	SpaceID   string   `json:"spaceId,omitempty"`
	SpaceName string   `json:"spaceName,omitempty"`
	UpdatedAt string   `json:"updatedAt,omitempty"`
	Source    string   `json:"source"`
}

// GlobalSearch returns only records that the current account can read. Each
// backing query performs its own Space/audience permission check so membership
// changes are respected on every request.
func (s *SpacesService) GlobalSearch() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		query := strings.TrimSpace(r.URL.Query().Get("q"))
		if query == "" {
			writeJSON(w, http.StatusOK, map[string]any{"hits": []globalSearchHit{}})
			return
		}
		limit := 40
		if parsed, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && parsed >= 1 && parsed <= 100 {
			limit = parsed
		}
		spaces, err := s.database.ListSpaces(r.Context(), userID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		lowerQuery := strings.ToLower(query)
		hits := make([]globalSearchHit, 0, limit)
		seenHits := map[string]bool{}
		appendHit := func(hit globalSearchHit) bool {
			if len(hits) >= limit {
				return false
			}
			if seenHits[hit.ID] {
				return true
			}
			seenHits[hit.ID] = true
			hit.AccountID = userID
			hit.Source = "server"
			hits = append(hits, hit)
			return len(hits) < limit
		}
		spaceNames := map[string]string{}
		for _, space := range spaces {
			spaceNames[space.ID] = space.Name
		}
		indexed, indexErr := s.database.SearchAIRetrieval(r.Context(), userID, query, nil, limit)
		if indexErr != nil {
			writeSpaceError(w, indexErr)
			return
		}
		for _, item := range indexed {
			kind := item.SourceKind
			if kind == "provider" {
				kind = "message"
			}
			if kind != "note" && kind != "task" && kind != "roadmap" && kind != "calendar" && kind != "message" {
				continue
			}
			if !appendHit(globalSearchHit{
				ID: item.SourceKind + ":" + item.SourceID, Kind: kind, Title: item.Title,
				Body: aiExcerpt(item.Content), Keywords: []string{kind, spaceNames[item.SpaceID]}, Href: item.Href,
				SpaceID: item.SpaceID, SpaceName: spaceNames[item.SpaceID], Source: "server",
			}) {
				break
			}
		}

		for _, space := range spaces {
			if strings.Contains(strings.ToLower(space.Name), lowerQuery) {
				if !appendHit(globalSearchHit{
					ID: "space:" + space.ID, Kind: "space", Title: space.Name,
					Body: space.Kind, Keywords: []string{"space", space.Name}, Href: "/spaces/" + url.PathEscape(space.ID),
					SpaceID: space.ID, SpaceName: space.Name, UpdatedAt: searchTime(space.UpdatedAt),
				}) {
					break
				}
			}
			if len(hits) >= limit {
				break
			}

			if space.Permissions[db.PermissionTasksView] {
				tasks, taskErr := s.database.SpaceTasks(r.Context(), userID, space.ID, db.SpaceTaskQuery{
					Search: query, Sort: "updated", Limit: min(8, limit-len(hits)),
				})
				if taskErr == nil {
					for _, task := range tasks {
						if !appendHit(globalSearchHit{
							ID: "task:" + task.ID, Kind: "task", Title: task.Title, Body: task.Notes,
							Keywords: []string{"task", task.TaskKey, task.Status, task.Priority, space.Name},
							Href:     "/spaces/" + url.PathEscape(space.ID) + "/planner/tasks/board?task=" + url.QueryEscape(task.ID),
							SpaceID:  space.ID, SpaceName: space.Name, UpdatedAt: searchTime(task.UpdatedAt),
						}) {
							break
						}
					}
				}
			}
			if len(hits) >= limit {
				break
			}

			notes, noteErr := s.database.AccessibleSpaceNotes(r.Context(), userID, space.ID)
			if noteErr == nil {
				for _, note := range notes {
					if !strings.Contains(strings.ToLower(note.TitleProjection), lowerQuery) {
						continue
					}
					if !appendHit(globalSearchHit{
						ID: "note:" + note.ID, Kind: "note", Title: note.TitleProjection, Body: "Note in " + space.Name,
						Keywords: []string{"note", space.Name}, Href: "/spaces/" + url.PathEscape(space.ID) + "/notes",
						SpaceID: space.ID, SpaceName: space.Name, UpdatedAt: searchTime(note.UpdatedAt),
					}) {
						break
					}
				}
			}
			if len(hits) >= limit {
				break
			}

			drawings, drawingErr := s.database.AccessibleSpaceDrawings(r.Context(), userID, space.ID)
			if drawingErr == nil {
				for _, drawing := range drawings {
					if !strings.Contains(strings.ToLower(drawing.Title), lowerQuery) {
						continue
					}
					if !appendHit(globalSearchHit{
						ID: "drawing:" + drawing.ID, Kind: "drawing", Title: drawing.Title,
						Body: "Drawing in " + space.Name, Keywords: []string{"drawing", space.Name},
						Href:    "/spaces/" + url.PathEscape(space.ID) + "/drawings/" + url.PathEscape(drawing.ID),
						SpaceID: space.ID, SpaceName: space.Name, UpdatedAt: searchTime(drawing.UpdatedAt),
					}) {
						break
					}
				}
			}
			if len(hits) >= limit {
				break
			}

			roadmaps, roadmapErr := s.database.SpaceRoadmaps(r.Context(), userID, space.ID)
			if roadmapErr == nil {
				for _, roadmap := range roadmaps {
					if !strings.Contains(strings.ToLower(roadmap.Name+" "+roadmap.Description), lowerQuery) {
						continue
					}
					if !appendHit(globalSearchHit{
						ID: "roadmap:" + roadmap.ID, Kind: "roadmap", Title: roadmap.Name, Body: roadmap.Description,
						Keywords: []string{"roadmap", space.Name},
						Href:     "/spaces/" + url.PathEscape(space.ID) + "/planner/roadmaps/" + url.PathEscape(roadmap.ID),
						SpaceID:  space.ID, SpaceName: space.Name, UpdatedAt: searchTime(roadmap.UpdatedAt),
					}) {
						break
					}
				}
			}
			if len(hits) >= limit {
				break
			}

			now := time.Now().UTC()
			events, calendarErr := s.database.SpaceCalendarEvents(r.Context(), userID, space.ID, now.AddDate(-1, 0, 0), now.AddDate(1, 0, 0))
			if calendarErr == nil {
				for _, event := range events {
					if !strings.Contains(strings.ToLower(event.Title+" "+event.Description+" "+event.Location), lowerQuery) {
						continue
					}
					if !appendHit(globalSearchHit{
						ID: "calendar:" + event.ID, Kind: "calendar", Title: event.Title, Body: event.Description,
						Keywords: []string{"calendar", "event", event.Location, space.Name},
						Href:     "/spaces/" + url.PathEscape(space.ID) + "/planner/agenda/day?date=" + url.QueryEscape(event.StartsAt.Format("2006-01-02")),
						SpaceID:  space.ID, SpaceName: space.Name, UpdatedAt: searchTime(event.UpdatedAt),
					}) {
						break
					}
				}
			}
			if len(hits) >= limit {
				break
			}

			conversations, conversationErr := s.database.SpaceConversations(r.Context(), userID, space.ID)
			if conversationErr == nil {
				for _, conversation := range conversations {
					if !strings.Contains(strings.ToLower(conversation.Title), lowerQuery) {
						continue
					}
					if !appendHit(globalSearchHit{
						ID: "conversation:" + conversation.ID, Kind: "conversation", Title: conversation.Title,
						Body: "Conversation in " + space.Name, Keywords: []string{"conversation", "chat", space.Name},
						Href:    "/spaces/" + url.PathEscape(space.ID) + "/chat?conversation=" + url.QueryEscape(conversation.ID),
						SpaceID: space.ID, SpaceName: space.Name, UpdatedAt: searchTime(conversation.UpdatedAt),
					}) {
						break
					}
				}
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"hits": hits})
	}
}

func searchTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

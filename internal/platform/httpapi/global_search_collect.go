package api

import (
	"context"
	"net/url"
	"strings"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// globalSearchCollector gathers de-duplicated hits up to the limit, applying the
// request's kind and Space filters.
type globalSearchCollector struct {
	userID      string
	limit       int
	kindFilter  map[string]bool
	spaceFilter string
	hits        []globalSearchHit
	seenHits    map[string]bool
}

func newGlobalSearchCollector(userID string, limit int, kindFilter map[string]bool, spaceFilter string) *globalSearchCollector {
	return &globalSearchCollector{
		userID:      userID,
		limit:       limit,
		kindFilter:  kindFilter,
		spaceFilter: spaceFilter,
		hits:        make([]globalSearchHit, 0, limit),
		seenHits:    map[string]bool{},
	}
}

// full reports whether the collector has reached its limit.
func (c *globalSearchCollector) full() bool {
	return len(c.hits) >= c.limit
}

// add keeps hit when it passes the filters and is not a duplicate. It returns
// false once the limit is reached, which tells callers to stop collecting.
func (c *globalSearchCollector) add(hit globalSearchHit) bool {
	if c.full() {
		return false
	}
	if len(c.kindFilter) > 0 && !c.kindFilter[hit.Kind] {
		return true
	}
	if c.spaceFilter != "" && hit.SpaceID != c.spaceFilter {
		return true
	}
	if hit.CanonicalID == "" {
		hit.CanonicalID = hit.Kind + ":" + strings.TrimPrefix(hit.ID, hit.Kind+":")
	}
	if c.seenHits[hit.CanonicalID] {
		return true
	}
	c.seenHits[hit.CanonicalID] = true
	hit.AccountID = c.userID
	hit.Source = "server"
	c.hits = append(c.hits, hit)
	return !c.full()
}

// collectIndexedHits adds AI retrieval results for the kinds the search shows.
func collectIndexedHits(c *globalSearchCollector, indexed []db.AIRetrievalHit, spaceNames map[string]string) {
	for _, item := range indexed {
		kind := item.SourceKind
		if kind == "provider" {
			kind = "message"
		}
		if kind != "note" && kind != "task" && kind != "roadmap" && kind != "calendar" && kind != "message" {
			continue
		}
		href := item.Href
		if kind == "note" && item.SpaceID != "" {
			href = "/spaces/" + url.PathEscape(item.SpaceID) + "/notes?note=" + url.QueryEscape(item.SourceID)
		}
		if !c.add(globalSearchHit{
			ID: item.SourceKind + ":" + item.SourceID, Kind: kind, Title: item.Title,
			Body: aiExcerpt(item.Content), Keywords: []string{kind, spaceNames[item.SpaceID]}, Href: href,
			SpaceID: item.SpaceID, SpaceName: spaceNames[item.SpaceID], Source: "server",
			CanonicalID: item.SourceKind + ":" + item.SourceID, Revision: item.SourceRevision,
			Score: normalizedSearchScore(item.Score), LexicalScore: normalizedSearchScore(item.LexicalScore), SemanticScore: normalizedSearchScore(item.SemanticScore),
		}) {
			break
		}
	}
}

// collectSpaceTasks adds task matches for one Space. The caller checks the
// tasks-view permission first.
func (s *SpacesService) collectSpaceTasks(ctx context.Context, c *globalSearchCollector, space db.Space, query string) {
	tasks, taskErr := s.database.SpaceTasks(ctx, c.userID, space.ID, db.SpaceTaskQuery{
		Search: query, Sort: "updated", Limit: min(8, c.limit-len(c.hits)),
	})
	if taskErr != nil {
		return
	}
	for _, task := range tasks {
		if !c.add(globalSearchHit{
			ID: "task:" + task.ID, Kind: "task", Title: task.Title, Body: task.Notes,
			Keywords: []string{"task", task.TaskKey, task.Status, task.Priority, space.Name},
			Href:     "/spaces/" + url.PathEscape(space.ID) + "/planner/tasks/board?task=" + url.QueryEscape(task.ID),
			SpaceID:  space.ID, SpaceName: space.Name, UpdatedAt: searchTime(task.UpdatedAt),
		}) {
			break
		}
	}
}

// collectSpaceNotes adds note titles in one Space that match the query.
func (s *SpacesService) collectSpaceNotes(ctx context.Context, c *globalSearchCollector, space db.Space, lowerQuery string) {
	notes, noteErr := s.database.AccessibleSpaceNotes(ctx, c.userID, space.ID)
	if noteErr != nil {
		return
	}
	for _, note := range notes {
		if !strings.Contains(strings.ToLower(note.TitleProjection), lowerQuery) {
			continue
		}
		if !c.add(globalSearchHit{
			ID: "note:" + note.ID, Kind: "note", Title: note.TitleProjection, Body: "Note in " + space.Name,
			Keywords: []string{"note", space.Name}, Href: "/spaces/" + url.PathEscape(space.ID) + "/notes?note=" + url.QueryEscape(note.ID),
			SpaceID: space.ID, SpaceName: space.Name, UpdatedAt: searchTime(note.UpdatedAt),
		}) {
			break
		}
	}
}

// collectSpaceDrawings adds drawing titles in one Space that match the query.
func (s *SpacesService) collectSpaceDrawings(ctx context.Context, c *globalSearchCollector, space db.Space, lowerQuery string) {
	drawings, drawingErr := s.database.AccessibleSpaceDrawings(ctx, c.userID, space.ID)
	if drawingErr != nil {
		return
	}
	for _, drawing := range drawings {
		if !strings.Contains(strings.ToLower(drawing.Title), lowerQuery) {
			continue
		}
		if !c.add(globalSearchHit{
			ID: "drawing:" + drawing.ID, Kind: "drawing", Title: drawing.Title,
			Body: "Drawing in " + space.Name, Keywords: []string{"drawing", space.Name},
			Href:    "/spaces/" + url.PathEscape(space.ID) + "/drawings/" + url.PathEscape(drawing.ID),
			SpaceID: space.ID, SpaceName: space.Name, UpdatedAt: searchTime(drawing.UpdatedAt),
		}) {
			break
		}
	}
}

// collectSpaceRoadmaps adds roadmaps in one Space whose name or description
// matches the query.
func (s *SpacesService) collectSpaceRoadmaps(ctx context.Context, c *globalSearchCollector, space db.Space, lowerQuery string) {
	roadmaps, roadmapErr := s.database.SpaceRoadmaps(ctx, c.userID, space.ID)
	if roadmapErr != nil {
		return
	}
	for _, roadmap := range roadmaps {
		if !strings.Contains(strings.ToLower(roadmap.Name+" "+roadmap.Description), lowerQuery) {
			continue
		}
		if !c.add(globalSearchHit{
			ID: "roadmap:" + roadmap.ID, Kind: "roadmap", Title: roadmap.Name, Body: roadmap.Description,
			Keywords: []string{"roadmap", space.Name},
			Href:     "/spaces/" + url.PathEscape(space.ID) + "/planner/roadmaps/" + url.PathEscape(roadmap.ID),
			SpaceID:  space.ID, SpaceName: space.Name, UpdatedAt: searchTime(roadmap.UpdatedAt),
		}) {
			break
		}
	}
}

// collectSpaceCalendarEvents adds events in the year around now whose title,
// description or location matches the query.
func (s *SpacesService) collectSpaceCalendarEvents(ctx context.Context, c *globalSearchCollector, space db.Space, lowerQuery string) {
	now := time.Now().UTC()
	events, calendarErr := s.database.SpaceCalendarEvents(ctx, c.userID, space.ID, now.AddDate(-1, 0, 0), now.AddDate(1, 0, 0))
	if calendarErr != nil {
		return
	}
	for _, event := range events {
		if !strings.Contains(strings.ToLower(event.Title+" "+event.Description+" "+event.Location), lowerQuery) {
			continue
		}
		if !c.add(globalSearchHit{
			ID: "calendar:" + event.ID, Kind: "calendar", Title: event.Title, Body: event.Description,
			Keywords: []string{"calendar", "event", event.Location, space.Name},
			Href:     "/spaces/" + url.PathEscape(space.ID) + "/planner/agenda/day?date=" + url.QueryEscape(event.StartsAt.Format("2006-01-02")),
			SpaceID:  space.ID, SpaceName: space.Name, UpdatedAt: searchTime(event.UpdatedAt),
		}) {
			break
		}
	}
}

// collectSpaceConversations adds conversation titles in one Space that match
// the query.
func (s *SpacesService) collectSpaceConversations(ctx context.Context, c *globalSearchCollector, space db.Space, lowerQuery string) {
	conversations, conversationErr := s.database.SpaceConversations(ctx, c.userID, space.ID)
	if conversationErr != nil {
		return
	}
	for _, conversation := range conversations {
		if !strings.Contains(strings.ToLower(conversation.Title), lowerQuery) {
			continue
		}
		if !c.add(globalSearchHit{
			ID: "conversation:" + conversation.ID, Kind: "conversation", Title: conversation.Title,
			Body: "Conversation in " + space.Name, Keywords: []string{"conversation", "chat", space.Name},
			Href:    "/spaces/" + url.PathEscape(space.ID) + "/chat?conversation=" + url.QueryEscape(conversation.ID),
			SpaceID: space.ID, SpaceName: space.Name, UpdatedAt: searchTime(conversation.UpdatedAt),
		}) {
			break
		}
	}
}

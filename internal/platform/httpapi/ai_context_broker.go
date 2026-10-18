package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

const (
	maxAIContextReferences = 64
	maxAISelectionBytes    = 32 << 10
)

type aiContextReference struct {
	Kind          string         `json:"kind"`
	ID            string         `json:"id"`
	Title         string         `json:"title"`
	Privacy       string         `json:"privacy"`
	SpaceID       string         `json:"space_id,omitempty"`
	Href          string         `json:"href,omitempty"`
	Revision      any            `json:"revision,omitempty"`
	OpaqueScopeID string         `json:"opaque_scope_id,omitempty"`
	Attached      bool           `json:"attached,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

type aiCitation struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Href     string `json:"href"`
	Revision any    `json:"revision,omitempty"`
	Excerpt  string `json:"excerpt,omitempty"`
}

type aiResolvedContext struct {
	Label    string
	Content  string
	Citation aiCitation
}

type aiContextBroker struct{ database *db.Database }

// retrieveAccount performs permission-first lexical retrieval across the
// collaborative records owned by their domain services. It is deliberately a
// broker method (rather than a global scan followed by filtering): membership,
// audience and lifecycle checks happen before a candidate can be ranked.
func (broker aiContextBroker) retrieveAccount(ctx context.Context, userID, query string, embedding []float64, limit int, spaceIDs ...string) ([]aiResolvedContext, error) {
	spaceID := ""
	if len(spaceIDs) > 0 {
		spaceID = spaceIDs[0]
	}
	query = strings.TrimSpace(query)
	if query == "" || limit <= 0 {
		return nil, nil
	}
	embeddingModel := "google/gemini-embedding-2"; if len(spaceIDs)>1 { embeddingModel=spaceIDs[1] }
 indexed, indexErr := broker.database.SearchAIRetrieval(ctx, userID, query, embedding, limit, spaceID, embeddingModel)
	if indexErr != nil {
		return nil, indexErr
	}
	if len(indexed) > 0 {
		resolved := make([]aiResolvedContext, 0, len(indexed))
		for _, hit := range indexed {
			resolved = append(resolved, aiResolvedContext{
				Label: "permission-filtered Misty index", Content: aiRelevantChunk(hit.Content, query),
				Citation: aiCitation{ID: hit.SourceID, Kind: hit.SourceKind, Title: hit.Title, Href: hit.Href, Revision: hit.SourceRevision, Excerpt: aiExcerpt(hit.Content)},
			})
		}
		return resolved, nil
	}
	spaces, err := broker.database.ListSpaces(ctx, userID)
	if err != nil {
		return nil, err
	}
	type candidate struct {
		score int
		item  aiResolvedContext
	}
	candidates := []candidate{}
	for _, space := range spaces {
		if spaceID != "" && space.ID != spaceID {
			continue
		}
		notes, noteErr := broker.database.AccessibleSpaceNotes(ctx, userID, space.ID)
		if noteErr == nil {
			for _, note := range notes {
				content := strings.TrimSpace(note.TitleProjection + "\n" + note.PlainTextProjection)
				score := aiSearchScore(query, content)
				if score == 0 {
					continue
				}
				candidates = append(candidates, candidate{score: score, item: aiResolvedContext{
					Label: "trusted Misty note", Content: aiRelevantChunk(content, query),
					Citation: aiCitation{ID: note.ID, Kind: "note", Title: firstAIText(note.TitleProjection, "Note"), Href: "/spaces/" + url.PathEscape(space.ID) + "/notes?note=" + url.QueryEscape(note.ID), Revision: note.CollaborationRevision, Excerpt: aiExcerpt(note.PlainTextProjection)},
				}})
			}
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })
	result := make([]aiResolvedContext, 0, min(limit, len(candidates)))
	seen := map[string]bool{}
	for _, candidate := range candidates {
		key := candidate.item.Citation.Kind + ":" + candidate.item.Citation.ID
		if candidate.score == 0 || seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, candidate.item)
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

func (broker aiContextBroker) resolve(ctx context.Context, userID string, references []aiContextReference) ([]aiResolvedContext, error) {
	if len(references) > maxAIContextReferences {
		return nil, errors.New("too many context references")
	}
	resolved := make([]aiResolvedContext, 0, len(references))
	seen := map[string]bool{}
	for _, reference := range references {
		reference.Kind = strings.ToLower(strings.TrimSpace(reference.Kind))
		reference.ID = strings.TrimSpace(reference.ID)
		if reference.Kind == "" || reference.ID == "" {
			return nil, errors.New("context references require kind and id")
		}
		identity := reference.Kind + ":" + reference.ID
		if seen[identity] {
			continue
		}
		seen[identity] = true
		item, ok, err := broker.resolveOne(ctx, userID, reference)
		if err != nil {
			return nil, err
		}
		if ok {
			resolved = append(resolved, item)
		}
	}
	return resolved, nil
}

func (broker aiContextBroker) resolveOne(ctx context.Context, userID string, reference aiContextReference) (aiResolvedContext, bool, error) {
	switch reference.Kind {
	case "note", "notes":
		note, err := broker.database.SpaceNoteByID(ctx, userID, reference.ID)
		if err != nil {
			return aiResolvedContext{}, false, err
		}
		if reference.SpaceID != "" && reference.SpaceID != note.SpaceID {
			return aiResolvedContext{}, false, db.ErrSpaceNotFound
		}
		title := firstAIText(note.TitleProjection, reference.Title, "Note")
		href := "/spaces/" + url.PathEscape(note.SpaceID) + "/notes?note=" + url.QueryEscape(note.ID)
		return aiResolvedContext{
			Label:    "trusted Misty note",
			Content:  strings.TrimSpace(note.PlainTextProjection),
			Citation: aiCitation{ID: note.ID, Kind: "note", Title: title, Href: href, Revision: note.CollaborationRevision, Excerpt: aiExcerpt(note.PlainTextProjection)},
		}, true, nil
	case "space.chat":
		spaceID := strings.TrimSpace(reference.SpaceID)
		if spaceID == "" {
			return aiResolvedContext{}, false, errors.New("chat context requires a Space")
		}
		var messages []db.SpaceMessage
		var err error
		if reference.ID == "everyone" || reference.ID == spaceID {
			messages, err = broker.database.SpaceMessages(ctx, userID, spaceID, 0, 100)
		} else {
			messages, err = broker.database.SpaceConversationMessages(ctx, userID, spaceID, reference.ID, 0, 100)
		}
		if err != nil {
			return aiResolvedContext{}, false, err
		}
		var content strings.Builder
		for index := len(messages) - 1; index >= 0; index-- {
			text := strings.TrimSpace(TestingSpansToPlainText(messages[index].Content))
			if text == "" {
				continue
			}
			fmt.Fprintf(&content, "%s — %s: %s\n", messages[index].CreatedAt.Format(time.RFC3339), messages[index].SenderName, text)
		}
		href := "/spaces/" + url.PathEscape(spaceID) + "/chat"
		if reference.ID != "everyone" && reference.ID != spaceID {
			href += "?conversation=" + url.QueryEscape(reference.ID)
		}
		return aiResolvedContext{
			Label: "trusted Misty Space conversation", Content: aiRelevantChunk(content.String(), reference.Title),
			Citation: aiCitation{ID: reference.ID, Kind: "space.chat", Title: firstAIText(reference.Title, "Space chat"), Href: href, Excerpt: "Recent messages in the visible conversation"},
		}, true, nil
	case "library.item":
		spaceID := strings.TrimSpace(reference.SpaceID)
		if spaceID == "" {
			return aiResolvedContext{}, false, errors.New("Library context requires a Space")
		}
		item, err := broker.database.LibraryItem(ctx, userID, spaceID, reference.ID)
		if err != nil {
			return aiResolvedContext{}, false, err
		}
		content := fmt.Sprintf("%s\nCaption: %s\nTags: %s\nOriginal filename: %s\nIntrinsic metadata: %s", item.DisplayName, item.Caption, strings.Join(item.Tags, ", "), item.File.OriginalFilename, string(item.File.IntrinsicMetadata))
		href := "/spaces/" + url.PathEscape(spaceID) + "/library?item=" + url.QueryEscape(item.ID)
		return aiResolvedContext{
			Label: "trusted Misty Library metadata", Content: aiRelevantChunk(content, reference.Title),
			Citation: aiCitation{ID: item.ID, Kind: "library.item", Title: item.DisplayName, Href: href, Revision: item.Version, Excerpt: aiExcerpt(item.Caption)},
		}, true, nil
	case "drawing":
		drawing, err := broker.database.SpaceDrawingByID(ctx, userID, reference.ID)
		if err != nil {
			return aiResolvedContext{}, false, err
		}
		if reference.SpaceID != "" && reference.SpaceID != drawing.SpaceID {
			return aiResolvedContext{}, false, db.ErrSpaceNotFound
		}
		href := "/spaces/" + url.PathEscape(drawing.SpaceID) + "/drawings/" + url.PathEscape(drawing.ID)
		content := fmt.Sprintf("Drawing: %s\nCollaboration revision: %d\nRole: %s", drawing.Title, drawing.CollaborationRevision, drawing.Role)
		return aiResolvedContext{
			Label: "trusted Misty drawing metadata", Content: content,
			Citation: aiCitation{ID: drawing.ID, Kind: "drawing", Title: drawing.Title, Href: href, Revision: drawing.CollaborationRevision, Excerpt: "Collaborative drawing metadata"},
		}, true, nil
	case "agent.artifact":
		return broker.resolveAgentArtifact(ctx, userID, reference)
	case "workspace.scope":
		// A container is a frozen list of source identities, not an eager read.
		// Each source still passes the normal permission checks when read by a tool.
		encoded, ok := reference.Metadata["members"].(string)
		if !ok || !json.Valid([]byte(encoded)) || len(encoded) > 64<<10 {
			return aiResolvedContext{}, false, errors.New("workspace scope is too large")
		}
		return aiResolvedContext{Label: "attached workspace search scope", Content: "Search only these attached source references. Contents have not been loaded: " + string(encoded), Citation: aiCitation{ID: reference.ID, Kind: reference.Kind, Title: reference.Title}}, true, nil
	case "route", "space":
		// Route labels are useful orientation, but never evidence that content was read.
		return aiResolvedContext{}, false, nil
	default:
		// Device/provider references are resolved only by their capability-bound
		// runtimes. Never turn their client labels into model-readable content.
		return aiResolvedContext{}, false, nil
	}
}

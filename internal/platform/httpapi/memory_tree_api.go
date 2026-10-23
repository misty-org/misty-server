package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/platform/db"
)

// MemoryTree lists the children of one node of the person's memory tree in
// one Space: GET ?space_id&parent&from&to. Without parent it returns the four
// branches. Ids are the ones MemoryForget accepts as node_id.
func (s *SpacesService) MemoryTree() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.memoryUser(w, r)
		if !ok {
			return
		}
		values := r.URL.Query()
		query := db.MemoryTreeQuery{SpaceID: memorySpaceParam(r), Parent: values.Get("parent")}
		for key, target := range map[string]*time.Time{"from": &query.From, "to": &query.To} {
			if raw := values.Get(key); raw != "" {
				parsed, err := time.Parse(time.RFC3339, raw)
				if err != nil {
					writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_memory_query"})
					return
				}
				*target = parsed
			}
		}
		space, err := requestMemorySpace(r.Context(), s.database, user, query.SpaceID)
		if err != nil {
			writeMemorySpaceError(w, err)
			return
		}
		query.SpaceID = space
		nodes, err := s.database.MemoryTree(r.Context(), user, query)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"space_id": space, "nodes": nodes})
	}
}

// MemoryNodeAdd keeps one memory the person writes by hand in a branch of the tree, in one Space:
// POST {space_id, branch, title, text, kind}. A moment is a note, a fact takes a kind (fact,
// preference or instruction), a summary is a recap that never runs, and a workflow is saved under
// the person's own Misty agent. It answers with the new node's id. The branches themselves never
// change.
func (s *SpacesService) MemoryNodeAdd() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.memoryUser(w, r)
		if !ok {
			return
		}
		var body struct {
			SpaceID     string `json:"space_id"`
			Branch      string `json:"branch"`
			Title       string `json:"title"`
			Description string `json:"description"`
			Text        string `json:"text"`
			Kind        string `json:"kind"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		if decodeAIJSON(w, r, &body) != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_memory_node"})
			return
		}
		space, err := requestMemorySpace(r.Context(), s.database, user, body.SpaceID)
		if err != nil {
			writeMemorySpaceError(w, err)
			return
		}
		body.Title, body.Text = strings.TrimSpace(body.Title), strings.TrimSpace(body.Text)
		id, err := s.addMemoryNode(r, user, space, body.Branch, body.Title, strings.TrimSpace(body.Description), body.Text, body.Kind)
		switch {
		case errors.Is(err, db.ErrMemoryBacklogFull):
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"code": "memory_backlog_full", "message": memoryBacklogMessage})
		case errors.Is(err, db.ErrSpaceInvalid):
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_memory_node", "message": "Check the text and try again."})
		case errors.Is(err, db.ErrSpaceConflict):
			writeJSON(w, http.StatusConflict, map[string]string{"code": "memory_not_kept", "message": "That can't be remembered. It may look sensitive, or Memory is off in your settings."})
		case err != nil:
			writeAgentError(w, err)
		default:
			writeJSON(w, http.StatusCreated, map[string]string{"id": id, "branch": body.Branch})
		}
	}
}

func (s *SpacesService) addMemoryNode(r *http.Request, user, space, branch, title, description, text, kind string) (string, error) {
	ctx := r.Context()
	switch branch {
	case db.MemoryMoments:
		if text == "" || len(text) > 20000 || len(title) > 300 {
			return "", db.ErrSpaceInvalid
		}
		if title == "" {
			title = firstLine(text, 80)
		}
		event := db.MemoryEvent{ID: uuid.NewString(), SpaceID: space, Kind: "note", Title: title, Body: text, OccurredAt: time.Now()}
		saved, err := s.database.RecordMemoryEvent(ctx, user, event, recallConfig().BacklogCap)
		return "moment:" + saved.ID, err
	case db.MemoryFacts:
		memory, err := s.database.RememberMistyMemory(ctx, user, db.RememberMistyMemoryInput{SpaceID: space, Kind: kind, Content: text, Reason: "Added by you"})
		return "fact:" + memory.ID, err
	case db.MemorySummaries:
		return s.database.AddManualSummary(ctx, user, space, title, text)
	case db.MemoryWorkflows:
		identity, err := s.database.EnsureAskIdentity(ctx, user, agent.FrontierDefaultModelID())
		if err != nil {
			return "", err
		}
		method := db.AgentMethod{SpaceID: space, AgentID: identity.ID, Kind: "workflow", Enabled: true, Definition: db.AgentMethodDefinition{
			Title: title, Description: description, Instructions: text, Target: "cloud", Inputs: []db.AgentMethodInput{}, RequiredTools: []string{},
		}}
		saved, err := s.database.SaveAgentMethod(ctx, user, method, 0)
		return "workflow:" + saved.ID, err
	}
	return "", db.ErrSpaceInvalid
}

func firstLine(text string, limit int) string {
	line, _, _ := strings.Cut(text, "\n")
	if r := []rune(strings.TrimSpace(line)); len(r) > limit {
		return string(r[:limit]) + "…"
	}
	return strings.TrimSpace(line)
}

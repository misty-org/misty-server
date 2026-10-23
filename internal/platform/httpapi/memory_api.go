package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/platform/config"
	"github.com/kannachi323/misty/server/internal/platform/db"
)

// RecallConfig is Memory on the server. It is off unless MISTY_RECALL=true.
// The backlog cap bounds how much may wait for an embedding per person.
type RecallConfig struct {
	Enabled    bool
	BacklogCap int
}

func recallConfig() RecallConfig {
	limit := 2000
	if parsed, err := strconv.Atoi(strings.TrimSpace(config.Getenv("MISTY_RECALL_BACKLOG_CAP"))); err == nil && parsed > 0 && parsed <= 100_000 {
		limit = parsed
	}
	return RecallConfig{Enabled: strings.EqualFold(strings.TrimSpace(config.Getenv("MISTY_RECALL")), "true"), BacklogCap: limit}
}

const memoryBacklogMessage = "Memory is catching up, so new pages aren't being kept for now. It continues on its own once it has caught up."

func (s *SpacesService) memoryUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	if !recallConfig().Enabled {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "memory_unavailable", "message": "Memory isn't available yet."})
		return "", false
	}
	return s.appsUser(w, r)
}

// writeMemorySpaceError answers a request for a Space the person isn't in.
func writeMemorySpaceError(w http.ResponseWriter, err error) {
	if errors.Is(err, db.ErrSpaceForbidden) || errors.Is(err, db.ErrSpaceNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "memory_space_not_found", "message": "That Space isn't one of yours."})
		return
	}
	writeAgentError(w, err)
}

// MemoryEvents records one event from the person's own device in the Space
// of the window it came from (POST, body space_id), or lists their events in
// one Space (GET ?space_id&from&to&site&kind&limit). With q, GET searches
// that Space's moments by meaning; there is no word search.
func (s *SpacesService) MemoryEvents() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.memoryUser(w, r)
		if !ok {
			return
		}
		if r.Method == http.MethodGet {
			query, text, valid := memoryQueryFrom(r)
			if !valid {
				writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_memory_query"})
				return
			}
			space, err := requestMemorySpace(r.Context(), s.database, user, memorySpaceParam(r))
			if err != nil {
				writeMemorySpaceError(w, err)
				return
			}
			query.SpaceID = space
			if text != "" {
				hits, err := s.searchMemory(r.Context(), user, space, []string{db.MemoryMoments}, text, query.Limit)
				if err != nil {
					writeAgentError(w, err)
					return
				}
				query.IDs = memoryHitIDs(hits)
				if len(query.IDs) == 0 {
					writeJSON(w, http.StatusOK, map[string]any{"events": []db.MemoryEvent{}})
					return
				}
			}
			events, err := s.database.MemoryEvents(r.Context(), user, query)
			if err != nil {
				writeAgentError(w, err)
				return
			}
			if text != "" {
				events = eventsInHitOrder(events, query.IDs)
			}
			writeJSON(w, http.StatusOK, map[string]any{"events": events})
			return
		}
		var event db.MemoryEvent
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		if decodeAIJSON(w, r, &event) != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_memory_event"})
			return
		}
		space, err := requestMemorySpace(r.Context(), s.database, user, event.SpaceID)
		if err != nil {
			writeMemorySpaceError(w, err)
			return
		}
		event.ID, event.SpaceID = uuid.NewString(), space
		if event.OccurredAt.IsZero() || event.OccurredAt.After(time.Now().Add(5*time.Minute)) {
			event.OccurredAt = time.Now()
		}
		saved, err := s.database.RecordMemoryEvent(r.Context(), user, event, recallConfig().BacklogCap)
		switch {
		case errors.Is(err, db.ErrMemoryBacklogFull):
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"code": "memory_backlog_full", "message": memoryBacklogMessage})
		case errors.Is(err, db.ErrSpaceInvalid):
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_memory_event"})
		case err != nil:
			writeAgentError(w, err)
		default:
			writeJSON(w, http.StatusCreated, map[string]string{"id": saved.ID, "same_as": saved.SameAs})
		}
	}
}

func memoryQueryFrom(r *http.Request) (db.MemoryQuery, string, bool) {
	values := r.URL.Query()
	query := db.MemoryQuery{Site: values.Get("site"), Kind: values.Get("kind")}
	text := strings.TrimSpace(values.Get("q"))
	for key, target := range map[string]*time.Time{"from": &query.From, "to": &query.To} {
		if raw := values.Get(key); raw != "" {
			parsed, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				return query, text, false
			}
			*target = parsed
		}
	}
	query.Limit, _ = strconv.Atoi(values.Get("limit"))
	return query, text, len(text) <= 500 && len(query.Site) <= 255
}

// MemoryForget deletes everything, one site, or a time range in one Space.
// POST {space_id, all} | {space_id, site} | {space_id, from, to}. With node_id
// it forgets one node of the memory tree instead (see ForgetMemoryNode), and
// with dry_run it only counts what that would forget.
func (s *SpacesService) MemoryForget() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.memoryUser(w, r)
		if !ok {
			return
		}
		var body struct {
			SpaceID string    `json:"space_id"`
			All     bool      `json:"all"`
			Site    string    `json:"site"`
			From    time.Time `json:"from"`
			To      time.Time `json:"to"`
			NodeID  string    `json:"node_id"`
			DryRun  bool      `json:"dry_run"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if decodeAIJSON(w, r, &body) != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_forget"})
			return
		}
		space, err := requestMemorySpace(r.Context(), s.database, user, body.SpaceID)
		if err != nil {
			writeMemorySpaceError(w, err)
			return
		}
		if body.NodeID != "" {
			counts, err := s.database.ForgetMemoryNode(r.Context(), user, space, body.NodeID, body.From, body.To, body.DryRun)
			if errors.Is(err, db.ErrSpaceInvalid) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_forget", "message": "That isn't something Memory can forget."})
				return
			}
			if err != nil {
				writeAgentError(w, err)
				return
			}
			key := "forgotten"
			if body.DryRun {
				key = "would_forget"
			}
			writeJSON(w, http.StatusOK, map[string]any{key: counts.Total(), "counts": counts})
			return
		}
		n, err := s.database.ForgetMemory(r.Context(), user, db.MemoryForget{SpaceID: space, All: body.All, Site: body.Site, From: body.From, To: body.To})
		if errors.Is(err, db.ErrSpaceInvalid) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_forget", "message": "Choose what to forget: everything, a site, or a time range."})
			return
		}
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]int64{"forgotten": n})
	}
}

// MemoryStatus says whether Memory is keeping up.
func (s *SpacesService) MemoryStatus() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.memoryUser(w, r)
		if !ok {
			return
		}
		waiting, err := s.database.MemoryBacklogCount(r.Context(), user)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		limit := recallConfig().BacklogCap
		status := map[string]any{"waiting": waiting, "cap": limit, "full": waiting >= limit}
		if waiting >= limit {
			status["message"] = memoryBacklogMessage
		}
		writeJSON(w, http.StatusOK, status)
	}
}

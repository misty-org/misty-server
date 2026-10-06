package api

import (
	"encoding/json"
	"net/http"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// message/stream and tasks/resubscribe follow one task over server-sent
// events: the Task first, then a status-update for each state change, the
// reply as an artifact-update, and a final status-update that ends the
// stream. A stream lasts at most a2aStreamLifetime; the client resubscribes
// to keep following a task that waits longer, for example for approval.
var (
	a2aStreamLifetime  = 15 * time.Minute
	a2aStreamPoll      = 5 * time.Second
	a2aStreamCheck     = time.Minute
	a2aStreamKeepalive = 25 * time.Second
)

func a2aStreamHeaders(w http.ResponseWriter) bool {
	if _, ok := w.(http.Flusher); !ok {
		return false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	return true
}

func writeA2AStreamEvent(w http.ResponseWriter, id json.RawMessage, result any) error {
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": a2aID(id), "result": result})
	if err != nil {
		return err
	}
	return writeAIInvocationSSE(w, "data: "+string(body)+"\n\n")
}

func (s *SpacesService) a2aStream(w http.ResponseWriter, r *http.Request, caller a2aCaller, id json.RawMessage, request *db.AgentMemberRequest) {
	if !a2aStreamHeaders(w) {
		writeA2AError(w, id, a2aUnsupportedFeature, "Streaming is unavailable on this connection")
		return
	}
	if writeA2AStreamEvent(w, id, a2aTask(request)) != nil {
		return
	}
	last, _, _ := a2aTaskStatus(request)
	if a2aTerminalState(last) {
		return
	}
	// Account events wake the stream when the task changes; the poll is the
	// fallback if the event service is unavailable or a hint is missed.
	events, unsubscribe, err := s.database.SubscribeAccountEvents(r.Context(), caller.UserID)
	if err == nil {
		defer unsubscribe()
	}
	poll := time.NewTicker(a2aStreamPoll)
	defer poll.Stop()
	check := time.NewTicker(a2aStreamCheck)
	defer check.Stop()
	keepalive := time.NewTicker(a2aStreamKeepalive)
	defer keepalive.Stop()
	lifetime := time.NewTimer(a2aStreamLifetime)
	defer lifetime.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-lifetime.C:
			return
		case <-keepalive.C:
			if writeAIInvocationSSE(w, ": keep-alive\n\n") != nil {
				return
			}
			continue
		case <-check.C:
			if authorized, err := s.a2aStillAuthorized(r, caller); err == nil && !authorized {
				return
			}
			continue
		case event := <-events:
			if event.Topic != "reset" && (event.Topic != "agent_requests" || (event.ID != "" && event.ID != request.ID)) {
				continue
			}
		case <-poll.C:
		}
		next, err := s.database.A2AAgentRequest(r.Context(), caller.UserID, caller.AgentID, request.ID)
		if err != nil {
			continue
		}
		state, status, reply := a2aTaskStatus(next)
		if state == last {
			continue
		}
		last = state
		final := a2aTerminalState(state)
		if reply != "" {
			if writeA2AStreamEvent(w, id, map[string]any{"kind": "artifact-update", "taskId": next.ID, "contextId": next.ID,
				"artifact": a2aReplyArtifact(next, reply), "lastChunk": true}) != nil {
				return
			}
		}
		if writeA2AStreamEvent(w, id, map[string]any{"kind": "status-update", "taskId": next.ID, "contextId": next.ID,
			"status": status, "final": final}) != nil || final {
			return
		}
	}
}

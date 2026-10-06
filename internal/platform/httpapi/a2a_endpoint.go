package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// The A2A endpoint lets a member's own agent talk to an agent another member
// published to a shared Space. Callers authenticate as one of their agents
// (see a2a_auth.go). It speaks A2A's JSON-RPC methods and sends every message
// through the same request service as agents_request, so consent, approval,
// billing and limits are identical.
const a2aProtocolVersion = "0.3.0"

// JSON-RPC and A2A error codes.
const (
	a2aParseError          = -32700
	a2aInvalidRequest      = -32600
	a2aMethodNotFound      = -32601
	a2aInvalidParams       = -32602
	a2aInternalError       = -32603
	a2aTaskNotFound        = -32001
	a2aTaskNotCancelable   = -32002
	a2aPushNotSupported    = -32003
	a2aUnsupportedFeature  = -32004
	a2aMaxRequestBodyBytes = 64 << 10
)

type a2aRPC struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type a2aPart struct {
	Kind string `json:"kind"`
	Text string `json:"text,omitempty"`
}

type a2aMessage struct {
	Role      string    `json:"role"`
	Parts     []a2aPart `json:"parts"`
	MessageID string    `json:"messageId"`
	ContextID string    `json:"contextId,omitempty"`
	TaskID    string    `json:"taskId,omitempty"`
	Kind      string    `json:"kind"`
}

type a2aSendParams struct {
	Message       a2aMessage `json:"message"`
	Configuration struct {
		PushNotificationConfig *a2aPushConfigInput `json:"pushNotificationConfig"`
	} `json:"configuration"`
}

// a2aError is a JSON-RPC error a handler returns.
type a2aError struct {
	Code    int
	Message string
}

func (e *a2aError) Error() string { return e.Message }

func a2aFail(code int, message string) error { return &a2aError{Code: code, Message: message} }

// A2AAgentCard describes one published agent to a member's agent.
func (s *SpacesService) A2AAgentCard() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller, ok := s.a2aAuthenticate(w, r)
		if !ok {
			return
		}
		listing, err := s.a2aListing(r, caller.UserID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		description := listing.Description
		if description == "" {
			description = listing.OwnerName + "’s agent in " + listing.SpaceName
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]any{
			"protocolVersion": a2aProtocolVersion, "name": listing.AgentName, "description": description,
			"url": a2aEndpointURL(r), "preferredTransport": "JSONRPC", "version": "1",
			"capabilities":      map[string]any{"streaming": true, "pushNotifications": true, "stateTransitionHistory": false},
			"defaultInputModes": []string{"text/plain"}, "defaultOutputModes": []string{"text/plain"},
			"skills": []map[string]any{{"id": "space-work", "name": "Work in " + listing.SpaceName,
				"description": description, "tags": []string{"misty", "space"}}},
			"securitySchemes": map[string]any{
				"mistySession": map[string]any{"type": "apiKey", "in": "cookie", "name": TestingSessionCookieName,
					"description": "The member's Misty account session."},
				"mistyAgent": map[string]any{"type": "apiKey", "in": "header", "name": a2aAgentTokenHeader,
					"description": "A five-minute token for one of the member's own agents, minted by the Misty app."},
			},
			"security": []map[string][]string{{"mistySession": {}, "mistyAgent": {}}},
			"provider": map[string]any{"organization": "Misty", "url": "https://misty.app"},
		})
	}
}

// A2AAgent handles the JSON-RPC methods. message/stream and tasks/resubscribe
// answer with a server-sent event stream; everything else with one response.
func (s *SpacesService) A2AAgent() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller, ok := s.a2aAuthenticate(w, r)
		if !ok {
			return
		}
		var call a2aRPC
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, a2aMaxRequestBodyBytes)).Decode(&call); err != nil {
			writeA2AError(w, nil, a2aParseError, "Parse error")
			return
		}
		if call.JSONRPC != "2.0" || call.Method == "" {
			writeA2AError(w, call.ID, a2aInvalidRequest, "Invalid Request")
			return
		}
		listing, err := s.a2aListing(r, caller.UserID)
		if err != nil {
			writeA2AError(w, call.ID, a2aTaskNotFound, "This agent is not published to a Space you share")
			return
		}
		var result any
		switch call.Method {
		case "message/send", "message/stream":
			var request *db.AgentMemberRequest
			request, err = s.a2aSend(r, caller, listing, call.Params)
			if err == nil && call.Method == "message/stream" {
				s.a2aStream(w, r, caller, call.ID, request)
				return
			}
			if err == nil {
				result = a2aTask(request)
			}
		case "tasks/get", "tasks/cancel", "tasks/resubscribe":
			var request *db.AgentMemberRequest
			request, err = s.a2aTaskCall(r, caller, listing, call.Method, call.Params)
			if err == nil && call.Method == "tasks/resubscribe" {
				s.a2aStream(w, r, caller, call.ID, request)
				return
			}
			if err == nil {
				result = a2aTask(request)
			}
		case "tasks/pushNotificationConfig/set", "tasks/pushNotificationConfig/get",
			"tasks/pushNotificationConfig/list", "tasks/pushNotificationConfig/delete":
			result, err = s.a2aPushConfigCall(r, caller, listing, call.Method, call.Params)
		default:
			writeA2AError(w, call.ID, a2aMethodNotFound, "Method not found")
			return
		}
		var failure *a2aError
		switch {
		case errors.As(err, &failure):
			writeA2AError(w, call.ID, failure.Code, failure.Message)
		case errors.Is(err, db.ErrSpaceNotFound):
			writeA2AError(w, call.ID, a2aTaskNotFound, "Task not found")
		case errors.Is(err, db.ErrSpaceConflict):
			writeA2AError(w, call.ID, a2aTaskNotCancelable, "Task cannot be canceled")
		case errors.Is(err, db.ErrSpaceInvalid):
			writeA2AError(w, call.ID, a2aInvalidParams, "Invalid params")
		case err != nil:
			writeA2AError(w, call.ID, a2aInternalError, agentRequestError(err, *listing).Error())
		default:
			writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": a2aID(call.ID), "result": result})
		}
	}
}

func (s *SpacesService) a2aListing(r *http.Request, userID string) (*db.SpaceAgentListing, error) {
	listings, err := s.database.SpaceAgentListings(r.Context(), userID, chi.URLParam(r, "spaceID"))
	if err != nil {
		return nil, err
	}
	for index := range listings {
		if listings[index].AgentID == chi.URLParam(r, "agentID") {
			return &listings[index], nil
		}
	}
	return nil, db.ErrSpaceNotFound
}

func (s *SpacesService) a2aSend(r *http.Request, caller a2aCaller, listing *db.SpaceAgentListing, raw json.RawMessage) (*db.AgentMemberRequest, error) {
	var params a2aSendParams
	if json.Unmarshal(raw, &params) != nil || strings.TrimSpace(params.Message.MessageID) == "" {
		return nil, db.ErrSpaceInvalid
	}
	if params.Message.TaskID != "" {
		// A request is one task; follow-ups start a new one.
		return nil, a2aFail(a2aUnsupportedFeature, "A task takes one message; send a new message to start another task")
	}
	push := params.Configuration.PushNotificationConfig
	if push != nil {
		if err := validateA2APushConfig(r, push); err != nil {
			return nil, err
		}
	}
	var text strings.Builder
	for _, part := range params.Message.Parts {
		if part.Kind == "text" {
			text.WriteString(part.Text)
		}
	}
	request, err := s.database.CreateAgentMemberRequest(r.Context(), db.AgentMemberRequestInput{
		SpaceID: listing.SpaceID, RequesterUserID: caller.UserID, RequesterAgentID: caller.AgentID,
		RequesterRunID: db.ExternalAgentRequester(caller.UserID, caller.AgentID),
		TargetAgentID:  listing.AgentID, Message: text.String(), IdempotencyKey: params.Message.MessageID,
	})
	if err != nil {
		return nil, err
	}
	if request.Replay && (request.TargetAgentID != listing.AgentID || request.SpaceID != listing.SpaceID) {
		return nil, a2aFail(a2aInvalidParams, "This messageId already started a task with another agent")
	}
	if push != nil && !request.Replay {
		if _, err := s.setA2APushConfig(r, caller, request.ID, push); err != nil {
			return nil, err
		}
	}
	return request, nil
}

// a2aTaskCall loads (or cancels) a task this agent sent to this listing.
func (s *SpacesService) a2aTaskCall(r *http.Request, caller a2aCaller, listing *db.SpaceAgentListing, method string, raw json.RawMessage) (*db.AgentMemberRequest, error) {
	var params struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &params) != nil || strings.TrimSpace(params.ID) == "" {
		return nil, db.ErrSpaceInvalid
	}
	request, err := s.a2aOwnTask(r, caller, listing, params.ID)
	if err != nil || method != "tasks/cancel" {
		return request, err
	}
	return s.database.CancelAgentMemberRequest(r.Context(), caller.UserID, request.ID)
}

func (s *SpacesService) a2aOwnTask(r *http.Request, caller a2aCaller, listing *db.SpaceAgentListing, taskID string) (*db.AgentMemberRequest, error) {
	request, err := s.database.A2AAgentRequest(r.Context(), caller.UserID, caller.AgentID, strings.TrimSpace(taskID))
	if err != nil {
		return nil, err
	}
	if request.TargetAgentID != listing.AgentID || request.SpaceID != listing.SpaceID {
		return nil, db.ErrSpaceNotFound
	}
	return request, nil
}

// a2aTaskStatus maps a request onto an A2A task state, status and reply.
func a2aTaskStatus(request *db.AgentMemberRequest) (state string, status map[string]any, reply string) {
	state = memberRequestState(request)
	switch state {
	case "awaiting_approval":
		state = "auth-required"
	case "failed":
		if request.RunState == "rejected" {
			state = "rejected"
		}
	}
	var view struct {
		Reply string `json:"reply"`
		Error string `json:"error"`
		Note  string `json:"note"`
	}
	_ = json.Unmarshal(agentRequestView(request), &view)
	status = map[string]any{"state": state, "timestamp": time.Now().UTC().Format(time.RFC3339)}
	if note := strings.TrimSpace(view.Error + view.Note); note != "" {
		status["message"] = map[string]any{"kind": "message", "role": "agent", "messageId": request.ID + ":status:" + state,
			"taskId": request.ID, "contextId": request.ID, "parts": []a2aPart{{Kind: "text", Text: note}}}
	}
	return state, status, view.Reply
}

func a2aTerminalState(state string) bool {
	switch state {
	case "completed", "failed", "canceled", "rejected":
		return true
	}
	return false
}

func a2aReplyArtifact(request *db.AgentMemberRequest, reply string) map[string]any {
	return map[string]any{"artifactId": request.ID + ":reply", "name": "reply", "parts": []a2aPart{{Kind: "text", Text: reply}}}
}

// a2aTask is the A2A Task for a request.
func a2aTask(request *db.AgentMemberRequest) map[string]any {
	_, status, reply := a2aTaskStatus(request)
	task := map[string]any{"kind": "task", "id": request.ID, "contextId": request.ID, "status": status}
	if reply != "" {
		task["artifacts"] = []map[string]any{a2aReplyArtifact(request, reply)}
	}
	return task
}

// a2aEndpointURL is the agent's absolute JSON-RPC address on the public API.
func a2aEndpointURL(r *http.Request) string {
	return a2aPublicURL(strings.TrimSuffix(r.URL.Path, "/.well-known/agent-card.json"))
}

func a2aPublicURL(path string) string {
	public, err := url.Parse(strings.TrimSpace(envconfig.Getenv("MISTY_PUBLIC_API_URL")))
	if err != nil || public.Scheme == "" || public.Host == "" {
		return path
	}
	return public.Scheme + "://" + public.Host + path
}

func a2aID(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return json.RawMessage(`null`)
	}
	return id
}

func writeA2AError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": a2aID(id), "error": map[string]any{"code": code, "message": message}})
}

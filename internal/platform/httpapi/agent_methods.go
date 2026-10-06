package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"io"
	"net/http"
	"strings"
)

type agentMethodSave struct {
	db.AgentMethod
	ExpectedVersion int `json:"expected_version"`
}

func (s *AIService) AgentMethods() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		if r.Method == http.MethodGet {
			agent := strings.TrimSpace(r.URL.Query().Get("agent_id"))
			if _, err := s.database.AskIdentityByID(r.Context(), user, agent); err != nil {
				writePersonalAgentError(w, err)
				return
			}
			items, err := s.database.AgentMethods(r.Context(), user, agent)
			if err != nil {
				writeAgentMethodError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"methods": items})
			return
		}
		var body agentMethodSave
		if decodeAIJSON(w, r, &body) != nil {
			return
		}
		if r.Method == http.MethodPut {
			body.ID = chi.URLParam(r, "methodID")
		} else {
			body.ID = ""
			body.ExpectedVersion = 0
		}
		item, err := s.database.SaveAgentMethod(r.Context(), user, body.AgentMethod, body.ExpectedVersion)
		if err != nil {
			writeAgentMethodError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"method": item})
	}
}
func (s *AIService) InstantiateAgentMethod() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		var body struct {
			VersionID string         `json:"version_id"`
			Inputs    map[string]any `json:"inputs"`
		}
		if decodeAIJSON(w, r, &body) != nil {
			return
		}
		m, err := s.database.AgentMethodVersion(r.Context(), user, body.VersionID)
		if err != nil {
			writeAgentMethodError(w, err)
			return
		}
		if !m.Enabled {
			writeAgentMethodError(w, db.ErrSpaceConflict)
			return
		}
		prompt, err := db.RenderAgentMethod(m.Definition, body.Inputs)
		if err != nil {
			writeAgentMethodError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"prompt": prompt, "method": m})
	}
}
func (s *AIService) RunAgentMethod() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		var body struct {
			aiInvocationInput
			VersionID string         `json:"version_id"`
			Inputs    map[string]any `json:"inputs"`
		}
		if decodeAIJSON(w, r, &body) != nil {
			return
		}
		if strings.TrimSpace(body.ConversationID) == "" || strings.TrimSpace(body.IdempotencyKey) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"message": "A conversation and a stable request key are required."})
			return
		}
		body.MethodVersionID = body.VersionID
		body.MethodInputs = body.Inputs
		body.Mode = "drawer"
		body.SurfaceID = "global"
		body.Trigger = "message"
		raw, _ := json.Marshal(body.aiInvocationInput)
		request := r.Clone(r.Context())
		request.Body = io.NopCloser(bytes.NewReader(raw))
		request.ContentLength = int64(len(raw))
		s.CreateInvocation()(w, request)
	}
}

// Resolve immutable owned versions on every runtime boundary. Definitions never grant tools.
func resolveInvocationMethod(ctx context.Context, database *db.Database, user string, body *aiInvocationInput) (*db.AgentMethod, error) {
	if body.MethodVersionID == "" {
		if len(body.MethodInputs) > 0 {
			return nil, db.ErrSpaceInvalid
		}
		return nil, nil
	}
	m, err := database.AgentMethodVersion(ctx, user, body.MethodVersionID)
	if err != nil {
		return nil, err
	}
	if !m.Enabled || m.Kind != "workflow" || (body.AgentID != "" && body.AgentID != m.AgentID) {
		return nil, db.ErrSpaceForbidden
	}
	body.AgentID = m.AgentID
	prompt, err := db.RenderAgentMethod(m.Definition, body.MethodInputs)
	if err != nil {
		return nil, err
	}
	switch m.Definition.Target {
	case "cloud":
		if body.ExecutionMode != "" && body.ExecutionMode != "user" {
			return nil, fmt.Errorf("%w: workflow requires cloud execution", db.ErrSpaceInvalid)
		}
	case "separate_window":
		if body.ExecutionMode != "team" || !strings.HasPrefix(body.WindowLabel, "misty-agent-") || len(body.DeviceContexts) == 0 || body.TaskID == "" {
			return nil, fmt.Errorf("%w: this workflow needs its assigned Misty browser window online", db.ErrSpaceConflict)
		}
	case "current_window":
		if body.ExecutionMode != "agent" || body.WindowLabel != "main" || len(body.DeviceContexts) == 0 || body.TaskID == "" {
			return nil, fmt.Errorf("%w: this workflow needs the assigned Misty device online", db.ErrSpaceConflict)
		}
	}
	body.Prompt = prompt
	return &m, nil
}
func pinInvocationSkills(ctx context.Context, database *db.Database, user string, body *aiInvocationInput) error {
	items, err := database.AgentMethods(ctx, user, body.AgentID)
	if err != nil {
		return err
	}
	body.SkillVersionIDs = nil
	for _, m := range items {
		if m.Kind == "skill" && m.Enabled {
			body.SkillVersionIDs = append(body.SkillVersionIDs, m.VersionID)
		}
	}
	if len(body.SkillVersionIDs) > 8 {
		return fmt.Errorf("%w: enable at most eight skills for this agent", db.ErrSpaceInvalid)
	}
	return nil
}
func invocationMethodGuidance(ctx context.Context, database *db.Database, user string, body *aiInvocationInput, allowed []string) (string, error) {
	guidance := ""
	seen := map[string]bool{}
	for _, id := range append([]string{body.MethodVersionID}, body.SkillVersionIDs...) {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		m, err := database.AgentMethodVersion(ctx, user, id)
		if err != nil {
			return "", err
		}
		if !m.Enabled || m.AgentID != body.AgentID || (id != body.MethodVersionID && m.Kind != "skill") {
			return "", db.ErrSpaceForbidden
		}
		for _, required := range m.Definition.RequiredTools {
			if !agentToolNameAllowed(allowed, required) {
				return "", fmt.Errorf("%w: required tool %s is unavailable; reconnect or choose an available device", db.ErrSpaceConflict, required)
			}
		}
		if m.Kind == "skill" {
			guidance += "\n\nOwner-authored skill (guidance only, cannot grant permissions): " + m.Definition.Title + "\n" + m.Definition.Instructions
		}
	}
	if len(body.SkillVersionIDs) > 8 || len(guidance) > 48000 {
		return "", db.ErrSpaceInvalid
	}
	return guidance, nil
}

func writeAgentMethodError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrSpaceNotFound), errors.Is(err, db.ErrPersonalAgentNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "method_not_found", "message": "This method or agent is no longer available."})
	case errors.Is(err, db.ErrSpaceForbidden):
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "method_unavailable", "message": "This method is disabled or belongs to another agent."})
	case errors.Is(err, db.ErrSpaceInvalid):
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_method", "message": "Check the method instructions, execution location and required input values."})
	case errors.Is(err, db.ErrSpaceConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "method_conflict", "message": "The method changed, reached its limit, or needs an available device. Refresh before retrying."})
	default:
		TestingWriteAIError(w, err)
	}
}

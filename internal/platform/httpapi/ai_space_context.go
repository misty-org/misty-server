package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	agent "github.com/kannachi323/misty/server/internal/agents"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// defaultSpaceContextSections is what a session with no personal agent reads.
// A personal agent narrows this to its owner-configured ContextPermissions.
//
// "task_notes" is the free-text notes column on a task, not the Notes surface.
// The Notes surface is device-local, so the server cannot read it at all; the
// capability card tells the agent to ask the member to paste instead.
var defaultSpaceContextSections = json.RawMessage(
	`{"space_chat":true,"library":true,"task_notes":true,"tasks":true,"members":true}`,
)

// applySpaceContext refreshes the Space blocks on an outgoing message request.
//
// Context used to be captured once, when the session was created, and never
// refreshed: ConfigureSession had exactly one caller. A task added a minute
// after the conversation started stayed invisible for the life of that
// conversation. This runs on every turn instead.
//
// It is deliberately cheap in the common case. SpaceContextRevision is one
// query; when it matches what the session already has, the records are left
// empty, the runtime reuses the previous turn's, and the prompt bytes stay
// byte-identical so the cached prefix survives.
func (s *AIService) applySpaceContext(
	ctx context.Context,
	userID, sessionID string,
	bound db.AgentSessionContext,
	request *agent.AgentMessageRequest,
) error {
	// Belt and braces: the field is informational, but a client sending a Space
	// other than the bound one is confused or probing, and silently substituting
	// ours would hide that.
	if request.SpaceID != "" && request.SpaceID != bound.SpaceID {
		return db.ErrSpaceForbidden
	}
	request.SpaceID = bound.SpaceID
	if bound.SpaceID == "" {
		return nil // Files-scoped session; there is no Space context to build.
	}

	revision, err := s.database.SpaceContextRevision(ctx, userID, bound.SpaceID)
	if err != nil {
		return err
	}
	state, err := s.runtime.SessionSpaceContext(ctx, sessionID, userID)
	if err != nil {
		return err
	}
	contextRevision := revision
	request.ContextTaskID = strings.TrimSpace(request.ContextTaskID)
	if request.ContextTaskID != "" {
		contextRevision += ":task:" + request.ContextTaskID
	}
	if state.Revision == contextRevision && state.HasCard {
		return nil // Nothing the agent can see has changed since the last turn.
	}

	sections := defaultSpaceContextSections
	if bound.AgentID != "" {
		sections, err = s.database.EffectivePersonalAgentContextPermissions(ctx, userID, bound.SpaceID, bound.AgentID)
		if err != nil {
			return err
		}
	}

	records, err := s.database.PersonalAgentSpaceContext(ctx, userID, bound.SpaceID, sections)
	if err != nil {
		return err
	}
	currentTask, err := resolvedCurrentTaskContext(ctx, s.database, userID, bound.SpaceID, request.ContextTaskID)
	if err != nil {
		return err
	}
	if currentTask != "" {
		records = strings.TrimSpace(records + "\n\n" + currentTask)
	}
	request.SpaceRecords = records
	request.SpaceContextRevision = contextRevision
	return nil
}

func resolvedCurrentTaskContext(ctx context.Context, database *db.Database, userID, spaceID, taskID string) (string, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return "", nil
	}
	task, err := database.SpaceTaskForMember(ctx, userID, spaceID, taskID)
	if err != nil {
		return "", err
	}
	lines := []string{fmt.Sprintf("Current Task: %s — %s (%s)", task.TaskKey, task.Title, task.Status)}
	if notes := strings.TrimSpace(task.Notes); notes != "" {
		lines = append(lines, "Current Task notes: "+notes)
	}
	if task.AssigneeUserID != "" {
		lines = append(lines, "Current Task assignee: member "+task.AssigneeUserID)
	} else if task.AssigneeAgentID != "" {
		lines = append(lines, "Current Task assignee: Agent "+task.AssigneeAgentID)
	}
	return strings.Join(lines, "\n"), nil
}

func TestingResolvedCurrentTaskContext(ctx context.Context, database *db.Database, userID, spaceID, taskID string) (string, error) {
	return resolvedCurrentTaskContext(ctx, database, userID, spaceID, taskID)
}

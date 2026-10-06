package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"strconv"
	"strings"
	"time"
)

// Plans an agent proposes in Plan mode, their versions and step progress.

var agentPlanRisks = map[string]bool{"read": true, "write": true, "draft": true, "consequential": true, "dangerous": true}
var agentPlanStepStatuses = map[string]bool{"pending": true, "in_progress": true, "done": true, "skipped": true, "blocked": true}

type AgentPlanStep struct {
	ID     string   `json:"id"`
	Title  string   `json:"title"`
	Detail string   `json:"detail,omitempty"`
	Tools  []string `json:"tools,omitempty"`
	Risk   string   `json:"risk"`
}

type AgentPlanPayload struct {
	Title           string          `json:"title"`
	Summary         string          `json:"summary"`
	Steps           []AgentPlanStep `json:"steps"`
	Assumptions     []string        `json:"assumptions"`
	SuccessCriteria []string        `json:"successCriteria"`
}

type AgentPlanStepProgress struct {
	Status    string    `json:"status"`
	Note      string    `json:"note,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type AgentPlan struct {
	ID             string                           `json:"id"`
	ConversationID string                           `json:"conversationId"`
	Version        int                              `json:"version"`
	RunID          string                           `json:"runId,omitempty"`
	Author         string                           `json:"author"`
	Payload        AgentPlanPayload                 `json:"plan"`
	Progress       map[string]AgentPlanStepProgress `json:"progress"`
	State          string                           `json:"state"`
	CreatedAt      time.Time                        `json:"createdAt"`
	ApprovedAt     *time.Time                       `json:"approvedAt,omitempty"`
	UpdatedAt      time.Time                        `json:"updatedAt"`
}

const agentPlanColumns = `id,conversation_id,version,run_id,author,payload,progress,state,created_at,approved_at,updated_at`

func scanAgentPlan(row interface{ Scan(...any) error }, out *AgentPlan) error {
	var payload, progress []byte
	if err := row.Scan(&out.ID, &out.ConversationID, &out.Version, &out.RunID, &out.Author, &payload, &progress, &out.State, &out.CreatedAt, &out.ApprovedAt, &out.UpdatedAt); err != nil {
		return err
	}
	out.Payload, out.Progress = AgentPlanPayload{}, map[string]AgentPlanStepProgress{}
	if err := json.Unmarshal(payload, &out.Payload); err != nil {
		return err
	}
	return json.Unmarshal(progress, &out.Progress)
}

// NormalizeAgentPlan trims and validates a plan. Step IDs are unique and stable
// so progress can follow them; missing IDs are assigned in order.
func NormalizeAgentPlan(plan AgentPlanPayload) (AgentPlanPayload, error) {
	plan.Title, plan.Summary = strings.TrimSpace(plan.Title), strings.TrimSpace(plan.Summary)
	if plan.Title == "" || len([]rune(plan.Title)) > 160 || len([]rune(plan.Summary)) > 1200 || len(plan.Steps) < 1 || len(plan.Steps) > MaxAgentPlanSteps {
		return plan, ErrSpaceInvalid
	}
	var err error
	if plan.Assumptions, err = trimmedList(plan.Assumptions, 10, 400); err != nil {
		return plan, err
	}
	if plan.SuccessCriteria, err = trimmedList(plan.SuccessCriteria, MaxGoalCriteria, 400); err != nil {
		return plan, err
	}
	seen := map[string]bool{}
	steps := make([]AgentPlanStep, 0, len(plan.Steps))
	for index, step := range plan.Steps {
		step.ID = strings.TrimSpace(step.ID)
		if step.ID == "" {
			step.ID = "step-" + strconv.Itoa(index+1)
		}
		step.Title, step.Detail, step.Risk = strings.TrimSpace(step.Title), strings.TrimSpace(step.Detail), strings.TrimSpace(step.Risk)
		if step.Risk == "" {
			step.Risk = "read"
		}
		if len(step.ID) > 40 || seen[step.ID] || step.Title == "" || len([]rune(step.Title)) > 120 || len([]rune(step.Detail)) > 4000 || !agentPlanRisks[step.Risk] {
			return plan, ErrSpaceInvalid
		}
		if step.Tools, err = trimmedList(step.Tools, 8, 80); err != nil {
			return plan, err
		}
		seen[step.ID] = true
		steps = append(steps, step)
	}
	plan.Steps = steps
	return plan, nil
}

// ProposeAgentPlan saves a new plan version and supersedes the current one.
// A replayed proposal from the same run with the same content returns it.
func (db *Database) ProposeAgentPlan(ctx context.Context, user, conversation, run, author string, payload AgentPlanPayload) (*AgentPlan, error) {
	if author != "agent" && author != "user" {
		return nil, ErrSpaceInvalid
	}
	normalized, err := NormalizeAgentPlan(payload)
	if err != nil {
		return nil, err
	}
	encoded, _ := json.Marshal(normalized)
	out := &AgentPlan{}
	err = db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		if err := conversationOwnedTx(ctx, tx, user, conversation); err != nil {
			return err
		}
		current := &AgentPlan{}
		err := scanAgentPlan(tx.QueryRowContext(ctx, `SELECT `+agentPlanColumns+` FROM agent_plans WHERE owner_user_id=$1 AND conversation_id=$2
			AND state IN ('proposed','approved') FOR UPDATE`, user, conversation), current)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			previous, _ := json.Marshal(current.Payload)
			if author == "agent" && run != "" && current.RunID == run && current.State == "proposed" && string(previous) == string(encoded) {
				*out = *current
				return nil
			}
			if _, err := tx.ExecContext(ctx, `UPDATE agent_plans SET state='superseded',updated_at=now() WHERE id=$1`, current.ID); err != nil {
				return err
			}
		}
		var version int
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0)+1 FROM agent_plans WHERE owner_user_id=$1 AND conversation_id=$2`, user, conversation).Scan(&version); err != nil {
			return err
		}
		return scanAgentPlan(tx.QueryRowContext(ctx, `INSERT INTO agent_plans(id,owner_user_id,conversation_id,version,run_id,author,payload,state)
			VALUES($1,$2,$3,$4,$5,$6,$7,'proposed') RETURNING `+agentPlanColumns,
			"plan_"+uuid.NewString(), user, conversation, version, run, author, encoded), out)
	})
	return out, err
}

// CurrentAgentPlan is the conversation's latest plan version, or nil.
func (db *Database) CurrentAgentPlan(ctx context.Context, user, conversation string) (*AgentPlan, error) {
	var out *AgentPlan
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		plan := &AgentPlan{}
		err := scanAgentPlan(tx.QueryRowContext(ctx, `SELECT `+agentPlanColumns+` FROM agent_plans WHERE owner_user_id=$1 AND conversation_id=$2
			ORDER BY version DESC LIMIT 1`, user, conversation), plan)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err == nil {
			out = plan
		}
		return err
	})
	return out, err
}

func (db *Database) AgentPlanByID(ctx context.Context, user, id string) (*AgentPlan, error) {
	out := &AgentPlan{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		return scanAgentPlan(tx.QueryRowContext(ctx, `SELECT `+agentPlanColumns+` FROM agent_plans WHERE id=$1 AND owner_user_id=$2`, id, user), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

// ApproveAgentPlan approves the exact version the user reviewed.
func (db *Database) ApproveAgentPlan(ctx context.Context, user, id string, version int) (*AgentPlan, error) {
	out := &AgentPlan{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		return scanAgentPlan(tx.QueryRowContext(ctx, `UPDATE agent_plans SET state='approved',approved_at=now(),updated_at=now()
			WHERE id=$1 AND owner_user_id=$2 AND version=$3 AND state='proposed' RETURNING `+agentPlanColumns, id, user, version), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceConflict
	}
	return out, err
}

// RejectAgentPlan sets a proposed plan aside without running it.
func (db *Database) RejectAgentPlan(ctx context.Context, user, id string) error {
	return db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE agent_plans SET state='rejected',updated_at=now() WHERE id=$1 AND owner_user_id=$2 AND state='proposed'`, id, user)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count != 1 {
			return ErrSpaceConflict
		}
		return nil
	})
}

type AgentPlanProgressUpdate struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Note   string `json:"note,omitempty"`
}

// UpdateAgentPlanProgress records step progress on the approved plan. Unknown
// step IDs are rejected; the plan completes when every step is done or skipped.
func (db *Database) UpdateAgentPlanProgress(ctx context.Context, user, conversation string, updates []AgentPlanProgressUpdate) (*AgentPlan, error) {
	if len(updates) < 1 || len(updates) > MaxAgentPlanSteps {
		return nil, ErrSpaceInvalid
	}
	out := &AgentPlan{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		current := &AgentPlan{}
		if err := scanAgentPlan(tx.QueryRowContext(ctx, `SELECT `+agentPlanColumns+` FROM agent_plans WHERE owner_user_id=$1 AND conversation_id=$2
			AND state='approved' FOR UPDATE`, user, conversation), current); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrSpaceConflict
			}
			return err
		}
		steps := map[string]bool{}
		for _, step := range current.Payload.Steps {
			steps[step.ID] = true
		}
		now := time.Now().UTC()
		for _, update := range updates {
			update.ID, update.Status, update.Note = strings.TrimSpace(update.ID), strings.TrimSpace(update.Status), strings.TrimSpace(update.Note)
			if !steps[update.ID] || !agentPlanStepStatuses[update.Status] || len([]rune(update.Note)) > 500 {
				return ErrSpaceInvalid
			}
			current.Progress[update.ID] = AgentPlanStepProgress{Status: update.Status, Note: update.Note, UpdatedAt: now}
		}
		complete := true
		for _, step := range current.Payload.Steps {
			status := current.Progress[step.ID].Status
			if status != "done" && status != "skipped" {
				complete = false
			}
		}
		state := "approved"
		if complete {
			state = "completed"
		}
		encoded, _ := json.Marshal(current.Progress)
		return scanAgentPlan(tx.QueryRowContext(ctx, `UPDATE agent_plans SET progress=$2,state=$3,updated_at=now() WHERE id=$1 RETURNING `+agentPlanColumns,
			current.ID, encoded, state), out)
	})
	return out, err
}

// AgentPlanVersion is one earlier version of a conversation's plan, for history.
type AgentPlanVersion struct {
	ID        string    `json:"id"`
	Version   int       `json:"version"`
	Author    string    `json:"author"`
	Title     string    `json:"title"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"createdAt"`
}

// AgentPlanHistory lists a conversation's plan versions, newest first.
func (db *Database) AgentPlanHistory(ctx context.Context, user, conversation string, limit int) ([]AgentPlanVersion, error) {
	if limit < 1 || limit > 50 {
		limit = 10
	}
	out := []AgentPlanVersion{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id,version,author,COALESCE(payload->>'title',''),state,created_at FROM agent_plans
			WHERE owner_user_id=$1 AND conversation_id=$2 ORDER BY version DESC LIMIT $3`, user, conversation, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AgentPlanVersion
			if err := rows.Scan(&item.ID, &item.Version, &item.Author, &item.Title, &item.State, &item.CreatedAt); err != nil {
				return err
			}
			out = append(out, item)
		}
		return rows.Err()
	})
	return out, err
}

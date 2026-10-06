package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"strings"
	"time"
)

// Goals a conversation pursues across turns, within a token budget.

type AgentGoal struct {
	ID                string          `json:"id"`
	ConversationID    string          `json:"conversationId"`
	Objective         string          `json:"objective"`
	SuccessCriteria   []string        `json:"successCriteria"`
	Status            string          `json:"status"`
	BudgetTokens      int64           `json:"budgetTokens"`
	UsedTokens        int64           `json:"usedTokens"`
	ContinuationCount int             `json:"continuationCount"`
	MaxContinuations  int             `json:"maxContinuations"`
	LastReport        json.RawMessage `json:"lastReport,omitempty"`
	LastInvocationID  string          `json:"-"`
	CreatedAt         time.Time       `json:"createdAt"`
	UpdatedAt         time.Time       `json:"updatedAt"`
}

const (
	DefaultGoalBudgetTokens  = int64(1_500_000)
	MinGoalBudgetTokens      = int64(10_000)
	MaxGoalBudgetTokens      = int64(50_000_000)
	DefaultGoalContinuations = 20
)

const agentGoalColumns = `id,conversation_id,objective,success_criteria,status,budget_tokens,used_tokens,continuation_count,max_continuations,last_report,last_invocation_id,created_at,updated_at`

func scanAgentGoal(row interface{ Scan(...any) error }, out *AgentGoal) error {
	var criteria, report []byte
	if err := row.Scan(&out.ID, &out.ConversationID, &out.Objective, &criteria, &out.Status, &out.BudgetTokens, &out.UsedTokens, &out.ContinuationCount, &out.MaxContinuations, &report, &out.LastInvocationID, &out.CreatedAt, &out.UpdatedAt); err != nil {
		return err
	}
	out.SuccessCriteria = []string{}
	if err := json.Unmarshal(criteria, &out.SuccessCriteria); err != nil {
		return err
	}
	out.LastReport = nil
	if len(report) > 0 {
		out.LastReport = append(json.RawMessage(nil), report...)
	}
	return nil
}

// SetAgentGoal replaces the conversation's active goal with a new pursued one.
func (db *Database) SetAgentGoal(ctx context.Context, user, conversation, objective string, criteria []string, budget int64) (*AgentGoal, error) {
	objective = strings.TrimSpace(objective)
	if objective == "" || len([]rune(objective)) > 2000 {
		return nil, ErrSpaceInvalid
	}
	if budget == 0 {
		budget = DefaultGoalBudgetTokens
	}
	if budget < MinGoalBudgetTokens || budget > MaxGoalBudgetTokens {
		return nil, ErrSpaceInvalid
	}
	criteria, err := trimmedList(criteria, MaxGoalCriteria, 400)
	if err != nil {
		return nil, err
	}
	encoded, _ := json.Marshal(criteria)
	out := &AgentGoal{}
	err = db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		if err := conversationOwnedTx(ctx, tx, user, conversation); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE agent_goals SET status='cleared',updated_at=now() WHERE owner_user_id=$1 AND conversation_id=$2 AND status IN ('pursuing','paused')`, user, conversation); err != nil {
			return err
		}
		return scanAgentGoal(tx.QueryRowContext(ctx, `INSERT INTO agent_goals(id,owner_user_id,conversation_id,objective,success_criteria,status,budget_tokens,max_continuations)
			VALUES($1,$2,$3,$4,$5,'pursuing',$6,$7) RETURNING `+agentGoalColumns,
			"goal_"+uuid.NewString(), user, conversation, objective, encoded, budget, DefaultGoalContinuations), out)
	})
	return out, err
}

// CurrentAgentGoal is the conversation's latest goal that was not cleared, or nil.
func (db *Database) CurrentAgentGoal(ctx context.Context, user, conversation string) (*AgentGoal, error) {
	var out *AgentGoal
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		goal := &AgentGoal{}
		err := scanAgentGoal(tx.QueryRowContext(ctx, `SELECT `+agentGoalColumns+` FROM agent_goals WHERE owner_user_id=$1 AND conversation_id=$2
			AND status<>'cleared' ORDER BY created_at DESC LIMIT 1`, user, conversation), goal)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err == nil {
			out = goal
		}
		return err
	})
	return out, err
}

// ControlAgentGoal is the user's control: pause, resume (with an optional larger
// budget) or clear. Resuming a goal that ran out of budget needs room to run.
func (db *Database) ControlAgentGoal(ctx context.Context, user, id, status string, budget int64) (*AgentGoal, error) {
	if status != "paused" && status != "pursuing" && status != "cleared" {
		return nil, ErrSpaceInvalid
	}
	if budget != 0 && (budget < MinGoalBudgetTokens || budget > MaxGoalBudgetTokens) {
		return nil, ErrSpaceInvalid
	}
	out := &AgentGoal{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		current := &AgentGoal{}
		if err := scanAgentGoal(tx.QueryRowContext(ctx, `SELECT `+agentGoalColumns+` FROM agent_goals WHERE id=$1 AND owner_user_id=$2 FOR UPDATE`, id, user), current); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrSpaceNotFound
			}
			return err
		}
		if current.Status == "cleared" || current.Status == "achieved" && status != "cleared" {
			return ErrSpaceConflict
		}
		if budget == 0 {
			budget = current.BudgetTokens
		}
		if status == "pursuing" && current.UsedTokens >= budget {
			return ErrSpaceConflict
		}
		continuations := current.ContinuationCount
		if status == "pursuing" && current.Status != "pursuing" {
			// A resumed goal gets a fresh run of continuations; spent tokens stay.
			continuations = 0
		}
		return scanAgentGoal(tx.QueryRowContext(ctx, `UPDATE agent_goals SET status=$3,budget_tokens=$4,continuation_count=$5,updated_at=now()
			WHERE id=$1 AND owner_user_id=$2 RETURNING `+agentGoalColumns, id, user, status, budget, continuations), out)
	})
	return out, err
}

// ReportAgentGoal records the agent's report on the conversation's pursued goal.
// Only pursued goals change; the caller has validated the report's evidence.
func (db *Database) ReportAgentGoal(ctx context.Context, user, conversation, status string, report json.RawMessage) (*AgentGoal, error) {
	if status != "pursuing" && status != "achieved" && status != "unmet" {
		return nil, ErrSpaceInvalid
	}
	out := &AgentGoal{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		return scanAgentGoal(tx.QueryRowContext(ctx, `UPDATE agent_goals SET status=$3,last_report=$4,updated_at=now()
			WHERE owner_user_id=$1 AND conversation_id=$2 AND status='pursuing' RETURNING `+agentGoalColumns, user, conversation, status, report), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceConflict
	}
	return out, err
}

// RecordAgentGoalRun adds one finished run's tokens to the conversation's active
// goal, once per invocation, and marks the goal budget_limited when spent.
func (db *Database) RecordAgentGoalRun(ctx context.Context, user, conversation, invocation string, tokens int64) (*AgentGoal, error) {
	if tokens < 0 {
		tokens = 0
	}
	var out *AgentGoal
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		goal := &AgentGoal{}
		err := scanAgentGoal(tx.QueryRowContext(ctx, `SELECT `+agentGoalColumns+` FROM agent_goals WHERE owner_user_id=$1 AND conversation_id=$2
			AND status IN ('pursuing','paused') FOR UPDATE`, user, conversation), goal)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if goal.LastInvocationID == invocation {
			out = goal
			return nil
		}
		used := goal.UsedTokens + tokens
		status := goal.Status
		if status == "pursuing" && used >= goal.BudgetTokens {
			status = "budget_limited"
		}
		updated := &AgentGoal{}
		if err := scanAgentGoal(tx.QueryRowContext(ctx, `UPDATE agent_goals SET used_tokens=$2,status=$3,last_invocation_id=$4,updated_at=now()
			WHERE id=$1 RETURNING `+agentGoalColumns, goal.ID, used, status, invocation), updated); err != nil {
			return err
		}
		out = updated
		return nil
	})
	return out, err
}

// ClaimAgentGoalContinuation reserves the next continuation for the goal after
// the run that just finished. It succeeds once per finished run.
func (db *Database) ClaimAgentGoalContinuation(ctx context.Context, user, id, finishedInvocation string) (bool, error) {
	claimed := false
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE agent_goals SET continuation_count=continuation_count+1,last_invocation_id='continued:'||$3,updated_at=now()
			WHERE id=$1 AND owner_user_id=$2 AND status='pursuing' AND last_invocation_id=$3 AND continuation_count<max_continuations AND used_tokens<budget_tokens`,
			id, user, finishedInvocation)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		claimed = count == 1
		return err
	})
	return claimed, err
}

// PauseAgentGoal stops a pursued goal on Misty's side, recording why, so the
// user can resume it. Only a pursued goal pauses.
func (db *Database) PauseAgentGoal(ctx context.Context, user, id, reason string) (*AgentGoal, error) {
	report, _ := json.Marshal(map[string]any{"status": "paused", "summary": strings.TrimSpace(reason), "reported_at": time.Now().UTC()})
	out := &AgentGoal{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		return scanAgentGoal(tx.QueryRowContext(ctx, `UPDATE agent_goals SET status='paused',last_report=$3,updated_at=now()
			WHERE id=$1 AND owner_user_id=$2 AND status='pursuing' RETURNING `+agentGoalColumns, id, user, report), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceConflict
	}
	return out, err
}

// MaxDailyGoalContinuations bounds automatic goal work per account across all
// conversations, on top of each goal's own budget and continuation limit.
const MaxDailyGoalContinuations = 100

// RecentGoalContinuations counts the account's goal continuations in the last day.
func (db *Database) RecentGoalContinuations(ctx context.Context, user string) (int, error) {
	count := 0
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM ai_invocations WHERE user_id=$1 AND trigger_kind='goal_continuation' AND created_at>now()-interval '24 hours'`, user).Scan(&count)
	})
	return count, err
}

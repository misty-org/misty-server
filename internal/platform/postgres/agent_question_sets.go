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

// Structured questions an agent asks inside a run, and the answers to them.

type AgentQuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type AgentQuestion struct {
	Header      string                `json:"header"`
	Question    string                `json:"question"`
	MultiSelect bool                  `json:"multiSelect"`
	Options     []AgentQuestionOption `json:"options"`
}

type AgentQuestionAnswer struct {
	Selected []string `json:"selected"`
	Other    string   `json:"other,omitempty"`
}

type AgentQuestionSet struct {
	ID             string                `json:"id"`
	RunID          string                `json:"runId"`
	CallID         string                `json:"-"`
	ConversationID string                `json:"conversationId"`
	Questions      []AgentQuestion       `json:"questions"`
	Answers        []AgentQuestionAnswer `json:"answers,omitempty"`
	State          string                `json:"state"`
	HandedOff      bool                  `json:"handedOff"`
	Continued      bool                  `json:"-"`
	CreatedAt      time.Time             `json:"createdAt"`
	AnsweredAt     *time.Time            `json:"answeredAt,omitempty"`
	ExpiresAt      time.Time             `json:"expiresAt"`
}

const agentQuestionColumns = `id,run_id,call_id,conversation_id,questions,answers,state,handed_off,continued,created_at,answered_at,expires_at`

func scanAgentQuestionSet(row interface{ Scan(...any) error }, out *AgentQuestionSet) error {
	var questions, answers []byte
	if err := row.Scan(&out.ID, &out.RunID, &out.CallID, &out.ConversationID, &questions, &answers, &out.State, &out.HandedOff, &out.Continued, &out.CreatedAt, &out.AnsweredAt, &out.ExpiresAt); err != nil {
		return err
	}
	out.Questions, out.Answers = nil, nil
	if err := json.Unmarshal(questions, &out.Questions); err != nil {
		return err
	}
	if len(answers) > 0 {
		return json.Unmarshal(answers, &out.Answers)
	}
	return nil
}

// NormalizeAgentQuestions trims and validates questions an agent asked. The
// limits mirror what the composer card can show.
func NormalizeAgentQuestions(questions []AgentQuestion) ([]AgentQuestion, error) {
	if len(questions) < 1 || len(questions) > MaxAgentQuestions {
		return nil, ErrSpaceInvalid
	}
	out := make([]AgentQuestion, 0, len(questions))
	for _, question := range questions {
		question.Header = strings.TrimSpace(question.Header)
		question.Question = strings.TrimSpace(question.Question)
		if question.Header == "" || len([]rune(question.Header)) > 24 || question.Question == "" || len([]rune(question.Question)) > 500 {
			return nil, ErrSpaceInvalid
		}
		if len(question.Options) < 2 || len(question.Options) > MaxAgentQuestionOptions {
			return nil, ErrSpaceInvalid
		}
		seen := map[string]bool{}
		options := make([]AgentQuestionOption, 0, len(question.Options))
		for _, option := range question.Options {
			option.Label = strings.TrimSpace(option.Label)
			option.Description = strings.TrimSpace(option.Description)
			key := strings.ToLower(option.Label)
			if option.Label == "" || len([]rune(option.Label)) > 80 || len([]rune(option.Description)) > 300 || seen[key] || key == "other" {
				return nil, ErrSpaceInvalid
			}
			seen[key] = true
			options = append(options, option)
		}
		question.Options = options
		out = append(out, question)
	}
	return out, nil
}

// OpenAgentQuestionSet records the questions one tool call asked. Replaying the
// same call returns the same set; a new call supersedes the run's open set.
func (db *Database) OpenAgentQuestionSet(ctx context.Context, user, run, call, conversation string, questions []AgentQuestion, ttl time.Duration) (*AgentQuestionSet, error) {
	if user == "" || run == "" || call == "" || len(call) > 200 || conversation == "" || ttl < time.Minute || ttl > 30*24*time.Hour {
		return nil, ErrSpaceInvalid
	}
	normalized, err := NormalizeAgentQuestions(questions)
	if err != nil {
		return nil, err
	}
	encoded, _ := json.Marshal(normalized)
	out := &AgentQuestionSet{}
	err = db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		if err := conversationOwnedTx(ctx, tx, user, conversation); err != nil {
			return err
		}
		err := scanAgentQuestionSet(tx.QueryRowContext(ctx, `SELECT `+agentQuestionColumns+` FROM agent_question_sets WHERE owner_user_id=$1 AND run_id=$2 AND call_id=$3`, user, run, call), out)
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE agent_question_sets SET state='superseded' WHERE owner_user_id=$1 AND run_id=$2 AND state='pending'`, user, run); err != nil {
			return err
		}
		return scanAgentQuestionSet(tx.QueryRowContext(ctx, `INSERT INTO agent_question_sets(id,owner_user_id,run_id,call_id,conversation_id,questions,state,expires_at)
			VALUES($1,$2,$3,$4,$5,$6,'pending',now()+$7*interval '1 second') RETURNING `+agentQuestionColumns,
			"question_"+uuid.NewString(), user, run, call, conversation, encoded, int(ttl/time.Second)), out)
	})
	return out, err
}

func (db *Database) AgentQuestionSet(ctx context.Context, user, id string) (*AgentQuestionSet, error) {
	out := &AgentQuestionSet{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE agent_question_sets SET state='expired' WHERE id=$1 AND owner_user_id=$2 AND state='pending' AND expires_at<=now()`, id, user); err != nil {
			return err
		}
		return scanAgentQuestionSet(tx.QueryRowContext(ctx, `SELECT `+agentQuestionColumns+` FROM agent_question_sets WHERE id=$1 AND owner_user_id=$2`, id, user), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

// ValidateAgentQuestionAnswers checks answers against the stored questions:
// one answer per question, only offered labels, one label unless multi-select,
// and something chosen or written for every question.
func ValidateAgentQuestionAnswers(questions []AgentQuestion, answers []AgentQuestionAnswer) ([]AgentQuestionAnswer, error) {
	if len(answers) != len(questions) {
		return nil, ErrSpaceInvalid
	}
	out := make([]AgentQuestionAnswer, 0, len(answers))
	for index, answer := range answers {
		question := questions[index]
		answer.Other = strings.TrimSpace(answer.Other)
		if len([]rune(answer.Other)) > 1000 || (!question.MultiSelect && len(answer.Selected) > 1) || len(answer.Selected) > len(question.Options) {
			return nil, ErrSpaceInvalid
		}
		offered := map[string]bool{}
		for _, option := range question.Options {
			offered[option.Label] = true
		}
		seen := map[string]bool{}
		selected := []string{}
		for _, label := range answer.Selected {
			if !offered[label] || seen[label] {
				return nil, ErrSpaceInvalid
			}
			seen[label] = true
			selected = append(selected, label)
		}
		if len(selected) == 0 && answer.Other == "" {
			return nil, ErrSpaceInvalid
		}
		out = append(out, AgentQuestionAnswer{Selected: selected, Other: answer.Other})
	}
	return out, nil
}

// AnswerAgentQuestionSet accepts the user's answers exactly once.
func (db *Database) AnswerAgentQuestionSet(ctx context.Context, user, id string, answers []AgentQuestionAnswer) (*AgentQuestionSet, error) {
	out := &AgentQuestionSet{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		current := &AgentQuestionSet{}
		if err := scanAgentQuestionSet(tx.QueryRowContext(ctx, `SELECT `+agentQuestionColumns+` FROM agent_question_sets WHERE id=$1 AND owner_user_id=$2 FOR UPDATE`, id, user), current); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrSpaceNotFound
			}
			return err
		}
		if current.State != "pending" || !current.ExpiresAt.After(time.Now()) {
			return ErrSpaceConflict
		}
		valid, err := ValidateAgentQuestionAnswers(current.Questions, answers)
		if err != nil {
			return err
		}
		encoded, _ := json.Marshal(valid)
		return scanAgentQuestionSet(tx.QueryRowContext(ctx, `UPDATE agent_question_sets SET state='answered',answers=$3,answered_at=now()
			WHERE id=$1 AND owner_user_id=$2 AND state='pending' RETURNING `+agentQuestionColumns, id, user, encoded), out)
	})
	return out, err
}

// HandOffAgentQuestionSet ends the asking run's wait. It returns the set: still
// pending (now handed off), or already answered, superseded or expired. The
// state check and the flag change are one statement, so an answer is either
// returned to the run or continues the conversation, never both or neither.
func (db *Database) HandOffAgentQuestionSet(ctx context.Context, user, id string) (*AgentQuestionSet, error) {
	out := &AgentQuestionSet{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		err := scanAgentQuestionSet(tx.QueryRowContext(ctx, `UPDATE agent_question_sets SET handed_off=true WHERE id=$1 AND owner_user_id=$2 AND state='pending'
			RETURNING `+agentQuestionColumns, id, user), out)
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		return scanAgentQuestionSet(tx.QueryRowContext(ctx, `SELECT `+agentQuestionColumns+` FROM agent_question_sets WHERE id=$1 AND owner_user_id=$2`, id, user), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

// ClaimAgentQuestionContinuation is true exactly once for an answered set whose
// run already handed off, so one answer continues its conversation at most once.
func (db *Database) ClaimAgentQuestionContinuation(ctx context.Context, user, id string) (bool, error) {
	claimed := false
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE agent_question_sets SET continued=true WHERE id=$1 AND owner_user_id=$2 AND state='answered' AND handed_off AND NOT continued`, id, user)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		claimed = count == 1
		return err
	})
	return claimed, err
}

// SetAgentQuestionState ends open sets: superseded by a new message, or
// canceled with their run. Only pending sets change.
func (db *Database) SetAgentQuestionState(ctx context.Context, user, conversation, run, state string) error {
	if state != "superseded" && state != "canceled" || conversation == "" && run == "" {
		return ErrSpaceInvalid
	}
	return db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE agent_question_sets SET state=$4 WHERE owner_user_id=$1 AND state='pending'
			AND ($2='' OR conversation_id=$2) AND ($3='' OR run_id=$3)`, user, conversation, run, state)
		return err
	})
}

// AgentQuestionSets lists a conversation's recent question sets, newest first.
func (db *Database) AgentQuestionSets(ctx context.Context, user, conversation string, limit int) ([]AgentQuestionSet, error) {
	if limit < 1 || limit > 50 {
		limit = 20
	}
	out := []AgentQuestionSet{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE agent_question_sets SET state='expired' WHERE owner_user_id=$1 AND conversation_id=$2 AND state='pending' AND expires_at<=now()`, user, conversation); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+agentQuestionColumns+` FROM agent_question_sets WHERE owner_user_id=$1 AND conversation_id=$2 ORDER BY created_at DESC LIMIT $3`, user, conversation, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AgentQuestionSet
			if err := scanAgentQuestionSet(rows, &item); err != nil {
				return err
			}
			out = append(out, item)
		}
		return rows.Err()
	})
	return out, err
}

func (db *Database) PendingAgentQuestionSet(ctx context.Context, user, conversation string) (*AgentQuestionSet, error) {
	sets, err := db.AgentQuestionSets(ctx, user, conversation, 5)
	if err != nil {
		return nil, err
	}
	for i := range sets {
		if sets[i].State == "pending" {
			return &sets[i], nil
		}
	}
	return nil, nil
}

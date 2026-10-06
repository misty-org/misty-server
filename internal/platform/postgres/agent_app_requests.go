package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// AgentAppRequest is something an agent needs from the user to continue:
// connecting an app, or approving one exact app action.
type AgentAppRequest struct {
	ID            string     `json:"id"`
	OwnerUserID   string     `json:"-"`
	RunID         string     `json:"run_id"`
	Kind          string     `json:"kind"`
	Subject       string     `json:"subject"`
	ArgumentsHash string     `json:"-"`
	Title         string     `json:"title"`
	Summary       string     `json:"summary"`
	State         string     `json:"state"`
	ExpiresAt     time.Time  `json:"expires_at"`
	CreatedAt     time.Time  `json:"created_at"`
	DecidedAt     *time.Time `json:"decided_at,omitempty"`
}

const agentAppRequestColumns = `id,owner_user_id,run_id,kind,subject,arguments_hash,title,summary,state,expires_at,created_at,decided_at`

func scanAgentAppRequest(row scanner, out *AgentAppRequest) error {
	return row.Scan(&out.ID, &out.OwnerUserID, &out.RunID, &out.Kind, &out.Subject, &out.ArgumentsHash, &out.Title, &out.Summary, &out.State, &out.ExpiresAt, &out.CreatedAt, &out.DecidedAt)
}

// OpenAgentAppRequest returns the live request for one exact need, or opens
// it. A recent decline of the same exact need is returned instead of asking
// again.
func (db *Database) OpenAgentAppRequest(ctx context.Context, request AgentAppRequest, ttl time.Duration) (*AgentAppRequest, error) {
	request.Title, request.Summary = strings.TrimSpace(request.Title), strings.TrimSpace(request.Summary)
	if request.OwnerUserID == "" || request.RunID == "" || (request.Kind != "connect" && request.Kind != "approve") || len(request.Subject) < 2 || len(request.Subject) > 128 ||
		request.Title == "" || len(request.Title) > 200 || len(request.Summary) > 4000 || ttl < time.Minute || ttl > time.Hour {
		return nil, ErrSpaceInvalid
	}
	out := &AgentAppRequest{}
	key := []any{request.OwnerUserID, request.Kind, request.Subject, request.ArgumentsHash}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(request.OwnerUserID), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE agent_app_requests SET state='expired' WHERE owner_user_id=$1 AND kind=$2 AND subject=$3 AND arguments_hash=$4
			AND state IN ('pending','approved') AND expires_at<=now()`, key...); err != nil {
			return err
		}
		for attempt := 0; attempt < 2; attempt++ {
			err := scanAgentAppRequest(tx.QueryRowContext(ctx, `SELECT `+agentAppRequestColumns+` FROM agent_app_requests
				WHERE owner_user_id=$1 AND kind=$2 AND subject=$3 AND arguments_hash=$4
				AND (state IN ('pending','approved') OR (state='declined' AND decided_at>now()-interval '15 minutes'))
				ORDER BY created_at DESC LIMIT 1`, key...), out)
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			inserted, err := tx.ExecContext(ctx, `INSERT INTO agent_app_requests(id,owner_user_id,run_id,kind,subject,arguments_hash,title,summary,state,expires_at)
				VALUES($1,$2,$3,$4,$5,$6,$7,$8,'pending',now()+$9*interval '1 second') ON CONFLICT DO NOTHING`,
				"apprq_"+uuid.NewString(), request.OwnerUserID, request.RunID, request.Kind, request.Subject, request.ArgumentsHash,
				request.Title, request.Summary, int(ttl/time.Second))
			if err != nil {
				return err
			}
			if count, _ := inserted.RowsAffected(); count == 0 {
				continue // Another call opened the same request first.
			}
		}
		return scanAgentAppRequest(tx.QueryRowContext(ctx, `SELECT `+agentAppRequestColumns+` FROM agent_app_requests
			WHERE owner_user_id=$1 AND kind=$2 AND subject=$3 AND arguments_hash=$4 AND state IN ('pending','approved')
			ORDER BY created_at DESC LIMIT 1`, key...), out)
	})
	return out, err
}

func (db *Database) AgentAppRequest(ctx context.Context, user, id string) (*AgentAppRequest, error) {
	out := &AgentAppRequest{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		return scanAgentAppRequest(tx.QueryRowContext(ctx, `SELECT `+agentAppRequestColumns+` FROM agent_app_requests WHERE id=$1 AND owner_user_id=$2`, id, user), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

// DecideAgentAppRequest records the user's answer. Connect requests can only
// be dismissed; approvals can be approved or declined.
func (db *Database) DecideAgentAppRequest(ctx context.Context, user, id, state string) (*AgentAppRequest, error) {
	if state != "approved" && state != "declined" {
		return nil, ErrSpaceInvalid
	}
	out := &AgentAppRequest{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		return scanAgentAppRequest(tx.QueryRowContext(ctx, `UPDATE agent_app_requests SET state=$3,decided_at=now()
			WHERE id=$1 AND owner_user_id=$2 AND state='pending' AND expires_at>now() AND (kind='approve' OR $3='declined')
			RETURNING `+agentAppRequestColumns, id, user, state), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceConflict
	}
	return out, err
}

// ResolveAgentAppRequest moves a request from one state to the next exactly
// once: a connect request to connected, an approval to used.
func (db *Database) ResolveAgentAppRequest(ctx context.Context, user, id, from, to string) (bool, error) {
	if !(from == "pending" && to == "connected") && !(from == "approved" && to == "used") {
		return false, ErrSpaceInvalid
	}
	resolved := false
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE agent_app_requests SET state=$4,decided_at=COALESCE(decided_at,now())
			WHERE id=$1 AND owner_user_id=$2 AND state=$3`, id, user, from, to)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		resolved = count == 1
		return err
	})
	return resolved, err
}

// ComposioSession returns the account's saved Composio session ID.
func (db *Database) ComposioSession(ctx context.Context, user string) (string, error) {
	var id string
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT session_id FROM composio_sessions WHERE owner_user_id=$1`, user).Scan(&id)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrSpaceNotFound
	}
	return id, err
}

func (db *Database) SaveComposioSession(ctx context.Context, user, session string) error {
	if len(session) < 4 || len(session) > 160 {
		return ErrSpaceInvalid
	}
	return db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO composio_sessions(owner_user_id,session_id) VALUES($1,$2)
			ON CONFLICT(owner_user_id) DO UPDATE SET session_id=EXCLUDED.session_id,updated_at=now()`, user, session)
		return err
	})
}

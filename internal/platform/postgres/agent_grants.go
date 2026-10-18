package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// AgentGrant is one remembered approval: this agent may take this action on
// this one resource of the grantor's without pausing, while the chat is in Auto.
type AgentGrant struct {
	ID              string          `json:"id"`
	AgentID         string          `json:"agent_id"`
	AgentName       string          `json:"agent_name"`
	GrantorUserID   string          `json:"-"`
	Action          string          `json:"action"`
	ScopeKind       string          `json:"scope_kind"`
	ScopeID         string          `json:"scope_id"`
	Constraints     json.RawMessage `json:"constraints"`
	SourceRequestID string          `json:"source_request_id,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	LastUsedAt      *time.Time      `json:"last_used_at,omitempty"`
	UseCount        int64           `json:"use_count"`
}

const agentGrantColumns = `g.id,g.agent_id,COALESCE(a.name,''),g.grantor_user_id,g.action,g.scope_kind,g.scope_id,g.constraints,
	g.source_request_id,g.created_at,g.last_used_at,g.use_count`

const agentGrantFrom = ` FROM agent_grants g LEFT JOIN misty_ask_identities a ON a.id=g.agent_id`

func scanAgentGrant(row scanner, out *AgentGrant) error {
	return row.Scan(&out.ID, &out.AgentID, &out.AgentName, &out.GrantorUserID, &out.Action, &out.ScopeKind, &out.ScopeID,
		&out.Constraints, &out.SourceRequestID, &out.CreatedAt, &out.LastUsedAt, &out.UseCount)
}

// ActiveAgentGrant is the unrevoked grant for one exact action and scope, or nil.
// Every permission decision reads it fresh, so a revoke applies to the next action.
func (db *Database) ActiveAgentGrant(ctx context.Context, grantor, agentID, action, scopeKind, scopeID string) (*AgentGrant, error) {
	if grantor == "" || agentID == "" || action == "" || scopeKind == "" || scopeID == "" {
		return nil, nil
	}
	out := &AgentGrant{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(grantor), func(tx *sql.Tx) error {
		return scanAgentGrant(tx.QueryRowContext(ctx, `SELECT `+agentGrantColumns+agentGrantFrom+`
			WHERE g.grantor_user_id=$1 AND g.agent_id=$2 AND g.action=$3 AND g.scope_kind=$4 AND g.scope_id=$5 AND g.revoked_at IS NULL`,
			grantor, agentID, action, scopeKind, scopeID), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return out, err
}

// TouchAgentGrant records that a grant let an action continue.
func (db *Database) TouchAgentGrant(ctx context.Context, grantor, id string) error {
	return db.TestingWithRLSContext(ctx, userRLSSettings(grantor), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE agent_grants SET last_used_at=now(),use_count=use_count+1
			WHERE id=$1 AND grantor_user_id=$2 AND revoked_at IS NULL`, id, grantor)
		return err
	})
}

// AgentGrants lists the grantor's active grants, newest first, optionally for one agent.
func (db *Database) AgentGrants(ctx context.Context, grantor, agentID string) ([]AgentGrant, error) {
	items := []AgentGrant{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(grantor), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT `+agentGrantColumns+agentGrantFrom+`
			WHERE g.grantor_user_id=$1 AND g.revoked_at IS NULL AND ($2='' OR g.agent_id=$2)
			ORDER BY g.created_at DESC, g.id LIMIT 500`, grantor, strings.TrimSpace(agentID))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AgentGrant
			if err := scanAgentGrant(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

// RevokeAgentGrant soft-revokes one grant.
func (db *Database) RevokeAgentGrant(ctx context.Context, grantor, id string) error {
	revoked := int64(0)
	err := db.TestingWithRLSContext(ctx, userRLSSettings(grantor), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE agent_grants SET revoked_at=now() WHERE id=$1 AND grantor_user_id=$2 AND revoked_at IS NULL`, id, grantor)
		if err == nil {
			revoked, err = result.RowsAffected()
		}
		return err
	})
	if err == nil && revoked == 0 {
		return ErrSpaceNotFound
	}
	return err
}

// RevokeAgentGrants soft-revokes every grant the grantor gave one agent.
func (db *Database) RevokeAgentGrants(ctx context.Context, grantor, agentID string) (int64, error) {
	if strings.TrimSpace(agentID) == "" {
		return 0, ErrSpaceInvalid
	}
	revoked := int64(0)
	err := db.TestingWithRLSContext(ctx, userRLSSettings(grantor), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE agent_grants SET revoked_at=now() WHERE agent_id=$1 AND grantor_user_id=$2 AND revoked_at IS NULL`, agentID, grantor)
		if err == nil {
			revoked, err = result.RowsAffected()
		}
		return err
	})
	return revoked, err
}

// RememberAgentAppRequest approves a pending, rememberable request and stores
// its grant in the same transaction. The action, scope and constraints come
// from the stored request, never from the person's answer.
func (db *Database) RememberAgentAppRequest(ctx context.Context, user, id string) (*AgentAppRequest, *AgentGrant, error) {
	request, grant := &AgentAppRequest{}, &AgentGrant{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		err := scanAgentAppRequest(tx.QueryRowContext(ctx, `UPDATE agent_app_requests SET state='approved',decided_at=now()
			WHERE id=$1 AND owner_user_id=$2 AND state='pending' AND expires_at>now() AND kind='approve' AND rememberable
			RETURNING `+agentAppRequestColumns, id, user), request)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceConflict
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO agent_grants(id,agent_id,grantor_user_id,action,scope_kind,scope_id,constraints,source_request_id)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (agent_id,grantor_user_id,action,scope_kind,scope_id) WHERE revoked_at IS NULL
			DO UPDATE SET constraints=EXCLUDED.constraints,source_request_id=EXCLUDED.source_request_id`,
			"grant_"+uuid.NewString(), request.AgentID, user, request.GrantAction, request.GrantScopeKind, request.GrantScopeID,
			request.GrantConstraints, request.ID); err != nil {
			return err
		}
		return scanAgentGrant(tx.QueryRowContext(ctx, `SELECT `+agentGrantColumns+agentGrantFrom+`
			WHERE g.grantor_user_id=$1 AND g.agent_id=$2 AND g.action=$3 AND g.scope_kind=$4 AND g.scope_id=$5 AND g.revoked_at IS NULL`,
			user, request.AgentID, request.GrantAction, request.GrantScopeKind, request.GrantScopeID), grant)
	})
	if err != nil {
		return nil, nil, err
	}
	return request, grant, nil
}

// ManagedMistyAgentID is the person's built-in Misty agent, or "" before it exists.
func (db *Database) ManagedMistyAgentID(ctx context.Context, user string) (string, error) {
	id := ""
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT id FROM misty_ask_identities
			WHERE owner_user_id=$1 AND system_managed AND runner='misty' AND deleted_at IS NULL`, user).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	})
	return id, err
}

// AgentDefaultRunMode is the agent's default Ask or Auto, or "" when the agent is unknown.
func (db *Database) AgentDefaultRunMode(ctx context.Context, user, agentID string) (string, error) {
	mode := ""
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT default_run_mode FROM misty_ask_identities
			WHERE id=$1 AND owner_user_id=$2 AND deleted_at IS NULL`, agentID, user).Scan(&mode)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	})
	return mode, err
}

// CreateAgentGrant stores a grant the person made directly, outside an app
// request: a page grant answered in the device's island. The caller validates
// the action and scope.
func (db *Database) CreateAgentGrant(ctx context.Context, grantor, agentID, action, scopeKind, scopeID string) (*AgentGrant, error) {
	out := &AgentGrant{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(grantor), func(tx *sql.Tx) error {
		var owned bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM misty_ask_identities WHERE id=$1 AND owner_user_id=$2 AND deleted_at IS NULL)`,
			agentID, grantor).Scan(&owned); err != nil {
			return err
		}
		if !owned {
			return ErrPersonalAgentNotFound
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO agent_grants(id,agent_id,grantor_user_id,action,scope_kind,scope_id)
			VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT (agent_id,grantor_user_id,action,scope_kind,scope_id) WHERE revoked_at IS NULL DO NOTHING`,
			"grant_"+uuid.NewString(), agentID, grantor, action, scopeKind, scopeID); err != nil {
			return err
		}
		return scanAgentGrant(tx.QueryRowContext(ctx, `SELECT `+agentGrantColumns+agentGrantFrom+`
			WHERE g.grantor_user_id=$1 AND g.agent_id=$2 AND g.action=$3 AND g.scope_kind=$4 AND g.scope_id=$5 AND g.revoked_at IS NULL`,
			grantor, agentID, action, scopeKind, scopeID), out)
	})
	return out, err
}

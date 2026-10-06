package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// The A2A endpoint is used by a member's own agents. Each call names the
// agent through a signed agent token; these queries confirm that agent still
// belongs to the member and scope every task and push config to it.

const a2aPushConfigsPerTask = 5

// a2aTerminalStates are the A2A task states that never change again.
const a2aTerminalStates = `'completed','failed','canceled','rejected'`

func requesterAgentActiveTx(ctx context.Context, tx *sql.Tx, userID, agentID string) (bool, error) {
	var active bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM misty_ask_identities
		WHERE id=$1 AND owner_user_id=$2 AND deleted_at IS NULL AND enabled)`, agentID, userID).Scan(&active)
	return active, err
}

// A2ARequesterAgentActive reports whether agentID is an enabled agent userID
// owns. Agent tokens are re-checked against it on every call, so deleting or
// disabling an agent stops its tokens at once.
func (db *Database) A2ARequesterAgentActive(ctx context.Context, userID, agentID string) (bool, error) {
	if userID == "" || agentID == "" {
		return false, nil
	}
	var active bool
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var err error
		active, err = requesterAgentActiveTx(ctx, tx, userID, agentID)
		return err
	})
	return active, err
}

// A2AAgentRequest returns a request one agent sent through the A2A endpoint.
// Requests other agents or runs sent are not found.
func (db *Database) A2AAgentRequest(ctx context.Context, userID, agentID, requestID string) (*AgentMemberRequest, error) {
	out := &AgentMemberRequest{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return scanAgentMemberRequest(tx.QueryRowContext(ctx, agentMemberRequestSelect+`WHERE q.id=$1 AND q.requester_user_id=$2 AND q.requester_run_id=$3`,
			requestID, userID, ExternalAgentRequester(userID, agentID)), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

type A2APushConfig struct {
	RequestID      string
	ID             string
	Token          string
	DeliveredState string
}

func a2aRequestOwnedTx(ctx context.Context, tx *sql.Tx, userID, agentID, requestID string) error {
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_member_requests WHERE id=$1 AND requester_user_id=$2 AND requester_run_id=$3)`,
		requestID, userID, ExternalAgentRequester(userID, agentID)).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrSpaceNotFound
	}
	return nil
}

// SetA2APushConfig adds or replaces one push config on a task the agent sent.
// Replacing a config delivers the task's current state to it again.
func (db *Database) SetA2APushConfig(ctx context.Context, userID, agentID, requestID, configID, token string) (A2APushConfig, error) {
	if configID == "" || len(configID) > 100 || len(token) > 512 {
		return A2APushConfig{}, ErrSpaceInvalid
	}
	config := A2APushConfig{RequestID: requestID, ID: configID, Token: token}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := a2aRequestOwnedTx(ctx, tx, userID, agentID, requestID); err != nil {
			return err
		}
		// Finished configs are kept a week so a reconnecting inbox can still
		// see what it was sent, then dropped.
		if _, err := tx.ExecContext(ctx, `DELETE FROM a2a_push_configs WHERE requester_user_id=$1
			AND delivered_state IN (`+a2aTerminalStates+`) AND delivered_at<NOW()-INTERVAL '7 days'`, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `SELECT 1 FROM agent_member_requests WHERE id=$1 FOR UPDATE`, requestID); err != nil {
			return err
		}
		var count int
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(bool_or(id=$2),false) FROM a2a_push_configs WHERE request_id=$1`, requestID, configID).Scan(&count, &exists); err != nil {
			return err
		}
		if !exists && count >= a2aPushConfigsPerTask {
			return ErrAgentRequestLimit
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO a2a_push_configs(request_id,id,requester_user_id,requester_agent_id,token) VALUES($1,$2,$3,$4,$5)
			ON CONFLICT (request_id,id) DO UPDATE SET token=EXCLUDED.token,delivered_state='',delivered_at=NULL`,
			requestID, configID, userID, agentID, token)
		return err
	})
	return config, err
}

// A2APushConfigs lists a task's push configs; configID narrows it to one.
func (db *Database) A2APushConfigs(ctx context.Context, userID, agentID, requestID, configID string) ([]A2APushConfig, error) {
	configs := []A2APushConfig{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := a2aRequestOwnedTx(ctx, tx, userID, agentID, requestID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT request_id,id,token,delivered_state FROM a2a_push_configs
			WHERE request_id=$1 AND requester_agent_id=$2 AND ($3='' OR id=$3) ORDER BY created_at,id`, requestID, agentID, configID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var config A2APushConfig
			if err := rows.Scan(&config.RequestID, &config.ID, &config.Token, &config.DeliveredState); err != nil {
				return err
			}
			configs = append(configs, config)
		}
		return rows.Err()
	})
	return configs, err
}

// DeleteA2APushConfig stops notifications for one config.
func (db *Database) DeleteA2APushConfig(ctx context.Context, userID, agentID, requestID, configID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := a2aRequestOwnedTx(ctx, tx, userID, agentID, requestID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM a2a_push_configs WHERE request_id=$1 AND id=$2 AND requester_agent_id=$3`, requestID, configID, agentID)
		if err != nil {
			return err
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return ErrSpaceNotFound
		}
		return nil
	})
}

// PendingA2APushConfigs lists an agent's configs whose task may still change,
// oldest first. requestID narrows it to one task.
func (db *Database) PendingA2APushConfigs(ctx context.Context, userID, agentID, requestID string, limit int) ([]A2APushConfig, error) {
	configs := []A2APushConfig{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT request_id,id,token,delivered_state FROM a2a_push_configs
			WHERE requester_user_id=$1 AND requester_agent_id=$2 AND ($3='' OR request_id=$3)
			AND delivered_state NOT IN (`+a2aTerminalStates+`) ORDER BY created_at,request_id,id LIMIT $4`, userID, agentID, requestID, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var config A2APushConfig
			if err := rows.Scan(&config.RequestID, &config.ID, &config.Token, &config.DeliveredState); err != nil {
				return err
			}
			configs = append(configs, config)
		}
		return rows.Err()
	})
	return configs, err
}

// ClaimA2APushDelivery records that state is being delivered to a config. It
// succeeds only if no other inbox delivered something since `from`, so two
// open inboxes for the same agent deliver each change once.
func (db *Database) ClaimA2APushDelivery(ctx context.Context, config A2APushConfig, state string) (bool, error) {
	var claimed bool
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE a2a_push_configs SET delivered_state=$4,delivered_at=$5
			WHERE request_id=$1 AND id=$2 AND delivered_state=$3`, config.RequestID, config.ID, config.DeliveredState, state, time.Now().UTC())
		if err != nil {
			return err
		}
		affected, _ := result.RowsAffected()
		claimed = affected == 1
		return nil
	})
	return claimed, err
}

// ReleaseA2APushDelivery undoes a claim whose write never reached the inbox,
// so the next inbox connection delivers it again.
func (db *Database) ReleaseA2APushDelivery(ctx context.Context, config A2APushConfig, state string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE a2a_push_configs SET delivered_state=$3 WHERE request_id=$1 AND id=$2 AND delivered_state=$4`,
			config.RequestID, config.ID, config.DeliveredState, state)
		return err
	})
}

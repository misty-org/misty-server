package db

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

var (
	// ErrCodeSessionBranchInUse is a new session on a branch an open session of the repo uses.
	ErrCodeSessionBranchInUse = errors.New("an open code session already uses this branch")
	// ErrCodeSessionEnded is ending a session that has already ended.
	ErrCodeSessionEnded = errors.New("code session already ended")
)

var (
	codeSessionBranchPattern     = regexp.MustCompile(`^misty/live/[a-z0-9][a-z0-9-]{0,62}$`)
	codeSessionBaseCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)
	codeSessionOutcomes          = map[string]bool{"merged": true, "pr": true, "kept": true, "discarded": true}
)

// CodeSessionPerson is who started a live code session.
type CodeSessionPerson struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// SpaceCodeSession is a live code session's record. The code itself lives in the session's
// code room and in each member's clone, never here. The JSON is the app's shape, as is.
type SpaceCodeSession struct {
	ID         string            `json:"id"`
	SpaceID    string            `json:"spaceId"`
	Name       string            `json:"name"`
	RepoKey    string            `json:"repoKey"`
	BaseCommit string            `json:"baseCommit"`
	Branch     string            `json:"branch"`
	StartedBy  CodeSessionPerson `json:"startedBy"`
	CreatedAt  time.Time         `json:"createdAt"`
	EndedAt    *time.Time        `json:"endedAt"`
	Outcome    *string           `json:"outcome"`
	// ACLVersion is what the session's room tickets carry; it never leaves the server.
	ACLVersion int64 `json:"-"`
}

// NewCodeSession is what a member sends to go live.
type NewCodeSession struct {
	Name       string
	RepoKey    string
	BaseCommit string
	Branch     string
}

func (input NewCodeSession) normalized() (NewCodeSession, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.RepoKey = strings.TrimSpace(input.RepoKey)
	input.BaseCommit = strings.ToLower(strings.TrimSpace(input.BaseCommit))
	input.Branch = strings.TrimSpace(input.Branch)
	if names := utf8.RuneCountInString(input.Name); names < 1 || names > 80 {
		return input, ErrSpaceInvalid
	}
	if keys := utf8.RuneCountInString(input.RepoKey); keys < 1 || keys > 512 {
		return input, ErrSpaceInvalid
	}
	if !codeSessionBaseCommitPattern.MatchString(input.BaseCommit) ||
		!codeSessionBranchPattern.MatchString(input.Branch) {
		return input, ErrSpaceInvalid
	}
	return input, nil
}

const codeSessionColumns = `c.id,c.space_id,c.name,c.repo_key,c.base_commit,c.branch,
	c.started_by_user_id,u.username,c.created_at,c.ended_at,c.outcome,c.acl_version`

type codeSessionScanner interface{ Scan(...any) error }

func scanCodeSession(row codeSessionScanner) (SpaceCodeSession, error) {
	var session SpaceCodeSession
	var endedAt sql.NullTime
	var outcome sql.NullString
	err := row.Scan(
		&session.ID, &session.SpaceID, &session.Name, &session.RepoKey, &session.BaseCommit,
		&session.Branch, &session.StartedBy.ID, &session.StartedBy.Name, &session.CreatedAt,
		&endedAt, &outcome, &session.ACLVersion,
	)
	if endedAt.Valid {
		ended := endedAt.Time
		session.EndedAt = &ended
	}
	if outcome.Valid {
		value := outcome.String
		session.Outcome = &value
	}
	return session, err
}

// CreateSpaceCodeSession opens a live session for any current member of the Space.
func (db *Database) CreateSpaceCodeSession(
	ctx context.Context,
	userID, spaceID string,
	input NewCodeSession,
) (*SpaceCodeSession, error) {
	input, err := input.normalized()
	if err != nil || userID == "" || spaceID == "" {
		return nil, ErrSpaceInvalid
	}
	var session SpaceCodeSession
	err = db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		var id string
		err := tx.QueryRowContext(ctx,
			`INSERT INTO space_code_sessions(id,space_id,started_by_user_id,name,repo_key,base_commit,branch)
			 VALUES($1,$2,$3,$4,$5,$6,$7)
			 ON CONFLICT (space_id,repo_key,branch) WHERE ended_at IS NULL DO NOTHING
			 RETURNING id`,
			"codesession_"+uuid.NewString(), spaceID, userID,
			input.Name, input.RepoKey, input.BaseCommit, input.Branch,
		).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrCodeSessionBranchInUse
		}
		if err != nil {
			return err
		}
		session, err = codeSessionByIDTx(ctx, tx, spaceID, id, false)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &session, nil
}

// OpenSpaceCodeSessions lists a Space's open sessions, newest first, for a current member.
func (db *Database) OpenSpaceCodeSessions(
	ctx context.Context,
	userID, spaceID string,
) ([]SpaceCodeSession, error) {
	sessions := []SpaceCodeSession{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx,
			`SELECT `+codeSessionColumns+`
			 FROM space_code_sessions c JOIN users u ON u.id=c.started_by_user_id
			 WHERE c.space_id=$1 AND c.ended_at IS NULL
			 ORDER BY c.created_at DESC, c.id`, spaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			session, err := scanCodeSession(rows)
			if err != nil {
				return err
			}
			sessions = append(sessions, session)
		}
		return rows.Err()
	})
	return sessions, err
}

// SpaceCodeSessionByID returns one session, ended ones too, to a current member. Anyone else,
// and a session of another Space, gets ErrSpaceNotFound, the same as a missing one.
func (db *Database) SpaceCodeSessionByID(
	ctx context.Context,
	userID, spaceID, sessionID string,
) (*SpaceCodeSession, error) {
	var session SpaceCodeSession
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireCodeSessionMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		var err error
		session, err = codeSessionByIDTx(ctx, tx, spaceID, sessionID, false)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &session, nil
}

// OpenSpaceCodeSessionForRoom returns a session a member may join: open, in this Space.
// An ended session is ErrSpaceNotFound, so its room can no longer be reached.
func (db *Database) OpenSpaceCodeSessionForRoom(
	ctx context.Context,
	userID, spaceID, sessionID string,
) (*SpaceCodeSession, error) {
	session, err := db.SpaceCodeSessionByID(ctx, userID, spaceID, sessionID)
	if err != nil {
		return nil, err
	}
	if session.EndedAt != nil {
		return nil, ErrSpaceNotFound
	}
	return session, nil
}

// EndSpaceCodeSession ends an open session with its outcome. Only the member who started it
// may; the session's room is purged through the outbox in the same transaction.
func (db *Database) EndSpaceCodeSession(
	ctx context.Context,
	userID, spaceID, sessionID, outcome string,
) (*SpaceCodeSession, error) {
	outcome = strings.TrimSpace(outcome)
	if !codeSessionOutcomes[outcome] {
		return nil, ErrSpaceInvalid
	}
	var session SpaceCodeSession
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireCodeSessionMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		current, err := codeSessionByIDTx(ctx, tx, spaceID, sessionID, true)
		if err != nil {
			return err
		}
		if current.StartedBy.ID != userID {
			return ErrSpaceForbidden
		}
		if current.EndedAt != nil {
			return ErrCodeSessionEnded
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE space_code_sessions SET ended_at=NOW(),outcome=$1 WHERE id=$2`,
			outcome, sessionID); err != nil {
			return err
		}
		if err := enqueueCodeSessionControlTx(ctx, tx, sessionID, "purge", nil); err != nil {
			return err
		}
		session, err = codeSessionByIDTx(ctx, tx, spaceID, sessionID, false)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &session, nil
}

// requireCodeSessionMemberTx checks membership for a session route. Someone outside the Space
// learns nothing about its sessions, so they get ErrSpaceNotFound.
func requireCodeSessionMemberTx(ctx context.Context, tx *sql.Tx, spaceID, userID string) error {
	if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
		if errors.Is(err, ErrSpaceForbidden) {
			return ErrSpaceNotFound
		}
		return err
	}
	return nil
}

func codeSessionByIDTx(
	ctx context.Context,
	tx *sql.Tx,
	spaceID, sessionID string,
	lock bool,
) (SpaceCodeSession, error) {
	query := `SELECT ` + codeSessionColumns + `
		FROM space_code_sessions c JOIN users u ON u.id=c.started_by_user_id
		WHERE c.id=$1 AND c.space_id=$2`
	if lock {
		query += ` FOR UPDATE OF c`
	}
	session, err := scanCodeSession(tx.QueryRowContext(ctx, query, sessionID, spaceID))
	if errors.Is(err, sql.ErrNoRows) {
		return SpaceCodeSession{}, ErrSpaceNotFound
	}
	return session, err
}

// revokeCodeSessionAccessForSpaceTx runs with every membership loss, as
// revokeDrawingAccessForSpaceTx does for drawings: each open session's room drops the sockets
// minted before, and the members still there reconnect with fresh tickets.
func revokeCodeSessionAccessForSpaceTx(ctx context.Context, tx *sql.Tx, spaceID string) error {
	rows, err := tx.QueryContext(ctx,
		`UPDATE space_code_sessions SET acl_version=acl_version+1
		 WHERE space_id=$1 AND ended_at IS NULL
		 RETURNING id,acl_version`, spaceID)
	if err != nil {
		return err
	}
	type revision struct {
		sessionID  string
		aclVersion int64
	}
	revisions := []revision{}
	for rows.Next() {
		var item revision
		if err := rows.Scan(&item.sessionID, &item.aclVersion); err != nil {
			rows.Close()
			return err
		}
		revisions = append(revisions, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, item := range revisions {
		if err := enqueueCodeSessionControlTx(ctx, tx, item.sessionID, "acl",
			map[string]any{"acl_version": item.aclVersion}); err != nil {
			return err
		}
	}
	return nil
}

// beginCodeSessionSpacePurgeTx ends a purging Space's open sessions and queues a room purge for
// every session that lacks one. It reports how many rooms have not confirmed their purge.
func beginCodeSessionSpacePurgeTx(ctx context.Context, tx *sql.Tx, spaceID string) (int, error) {
	if _, err := tx.ExecContext(ctx,
		`UPDATE space_code_sessions SET ended_at=NOW() WHERE space_id=$1 AND ended_at IS NULL`,
		spaceID); err != nil {
		return 0, err
	}
	sessions, err := spacePurgeIDsTx(ctx, tx, `SELECT c.id FROM space_code_sessions c WHERE c.space_id=$1
		AND NOT EXISTS(SELECT 1 FROM space_code_session_control_outbox o WHERE o.session_id=c.id AND o.command='purge')`, spaceID)
	if err != nil {
		return 0, err
	}
	for _, id := range sessions {
		if err := enqueueCodeSessionControlTx(ctx, tx, id, "purge", nil); err != nil {
			return 0, err
		}
	}
	var waiting int
	err = tx.QueryRowContext(ctx, `SELECT count(*) FROM space_code_sessions c WHERE c.space_id=$1
		AND NOT EXISTS(SELECT 1 FROM space_code_session_control_outbox o
		  WHERE o.session_id=c.id AND o.command='purge' AND o.delivered_at IS NOT NULL)`, spaceID).Scan(&waiting)
	return waiting, err
}

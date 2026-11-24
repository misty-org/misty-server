package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/lib/pq"

	"github.com/misty-org/misty-server/internal/collab/file"
	"github.com/misty-org/misty-server/internal/collab/history"
)

type collabFileState = file.State

func (s *CollabStore) SaveFileState(ctx context.Context, roomID string, state file.State) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return s.db.SpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO collab_file_state(room_id,state,updated_at) VALUES($1,$2,NOW())
			ON CONFLICT (room_id) DO UPDATE SET state=EXCLUDED.state,updated_at=EXCLUDED.updated_at`, roomID, raw)
		return err
	})
}

func (s *CollabStore) DeleteFileState(ctx context.Context, roomID string) error {
	return s.db.SpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM collab_file_state WHERE room_id=$1`, roomID)
		return err
	})
}

func (s *CollabStore) Versions(ctx context.Context, roomID string) ([]history.Version, error) {
	versions := []history.Version{}
	err := s.db.SpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id,created_at,byte_size,made_by,checksum FROM collab_history
			WHERE room_id=$1 ORDER BY created_at,id`, roomID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var version history.Version
			if err := rows.Scan(&version.ID, &version.At, &version.Size, &version.By, &version.Checksum); err != nil {
				return err
			}
			versions = append(versions, version)
		}
		return rows.Err()
	})
	return versions, err
}

func (s *CollabStore) AddVersion(ctx context.Context, roomID string, version history.Version, state []byte) error {
	return s.db.SpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO collab_history(id,room_id,created_at,byte_size,checksum,made_by,state)
			VALUES($1,$2,$3,$4,$5,$6,$7)`, version.ID, roomID, version.At, len(state), version.Checksum, version.By, state)
		return err
	})
}

func (s *CollabStore) ReadVersion(ctx context.Context, roomID, versionID string) (history.Version, []byte, error) {
	var version history.Version
	var state []byte
	err := s.db.SpaceTx(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT id,created_at,byte_size,made_by,checksum,state FROM collab_history
			WHERE room_id=$1 AND id=$2`, roomID, versionID).
			Scan(&version.ID, &version.At, &version.Size, &version.By, &version.Checksum, &state)
		if errors.Is(err, sql.ErrNoRows) {
			return history.ErrNoVersion
		}
		return err
	})
	if err != nil {
		return history.Version{}, nil, err
	}
	if len(state) != version.Size || collabChecksum(state) != version.Checksum {
		return history.Version{}, nil, history.ErrNoVersion
	}
	return version, state, nil
}

func (s *CollabStore) DeleteVersions(ctx context.Context, roomID string, versionIDs []string) error {
	if len(versionIDs) == 0 {
		return nil
	}
	return s.db.SpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM collab_history WHERE room_id=$1 AND id=ANY($2)`, roomID, pq.Array(versionIDs))
		return err
	})
}

func (s *CollabStore) HistoryDays(ctx context.Context, spaceID string) (int, error) {
	var days int
	err := s.db.SpaceTx(ctx, func(tx *sql.Tx) error {
		limits, err := spaceItemLimitsTx(ctx, tx, spaceID)
		if err != nil {
			return err
		}
		if limits.HistoryDays != nil {
			days = int(*limits.HistoryDays)
		}
		return nil
	})
	return days, err
}

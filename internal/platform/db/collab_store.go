package db

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/misty-org/misty-server/internal/collab"
)

const collabRoomColumns = `id,space_id,room_key,options,acl_version,purged,generation,owner_url`

type CollabStore struct {
	db     *Database
	OnLost func()

	lockMu   sync.Mutex
	lockConn *sql.Conn
	held     map[string]int
}

func (db *Database) CollabStore() *CollabStore {
	return &CollabStore{db: db, held: map[string]int{}}
}

func newCollabRoomID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	encoded := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw))
	return "rm_" + encoded[:26], nil
}

func scanCollabRoom(row interface{ Scan(...any) error }) (collab.Record, error) {
	var rec collab.Record
	var options []byte
	err := row.Scan(&rec.ID, &rec.SpaceID, &rec.Key, &options, &rec.ACLVersion, &rec.Purged, &rec.Generation, &rec.Owner)
	if errors.Is(err, sql.ErrNoRows) {
		return collab.Record{}, collab.ErrNoRoom
	}
	if err != nil {
		return collab.Record{}, err
	}
	rec.Options = collab.ParseStoredOptions(options)
	return rec, nil
}

func (s *CollabStore) FindRoom(ctx context.Context, spaceID, key string) (collab.Record, error) {
	var rec collab.Record
	err := s.db.SpaceTx(ctx, func(tx *sql.Tx) error {
		var err error
		rec, err = scanCollabRoom(tx.QueryRowContext(ctx, `SELECT `+collabRoomColumns+` FROM collab_rooms
			WHERE space_id=$1 AND room_key=$2`, spaceID, key))
		return err
	})
	return rec, err
}

func (s *CollabStore) RoomByID(ctx context.Context, roomID string) (collab.Record, error) {
	var rec collab.Record
	err := s.db.SpaceTx(ctx, func(tx *sql.Tx) error {
		var err error
		rec, err = scanCollabRoom(tx.QueryRowContext(ctx, `SELECT `+collabRoomColumns+` FROM collab_rooms WHERE id=$1`, roomID))
		return err
	})
	return rec, err
}

func (s *CollabStore) CreateRoom(ctx context.Context, spaceID, key string, options collab.Options) (collab.Record, error) {
	id, err := newCollabRoomID()
	if err != nil {
		return collab.Record{}, err
	}
	var rec collab.Record
	err = s.db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO collab_rooms(id,space_id,room_key,options)
			SELECT $1,$2,$3,$4 WHERE EXISTS(SELECT 1 FROM spaces WHERE id=$2 AND lifecycle_state='active')
			ON CONFLICT (space_id,room_key) DO NOTHING`, id, spaceID, key, []byte(options.JSON())); err != nil {
			return err
		}
		var err error
		rec, err = scanCollabRoom(tx.QueryRowContext(ctx, `SELECT `+collabRoomColumns+` FROM collab_rooms
			WHERE space_id=$1 AND room_key=$2`, spaceID, key))
		return err
	})
	return rec, err
}

func collabLockKey(roomID string) int64 {
	sum := sha256.Sum256([]byte("misty:collab-room:" + roomID))
	return int64(binary.BigEndian.Uint64(sum[:8]))
}

func (s *CollabStore) lockConnLocked(ctx context.Context) (*sql.Conn, error) {
	if s.lockConn != nil {
		return s.lockConn, nil
	}
	conn, err := s.db.Conn.Conn(ctx)
	if err != nil {
		return nil, err
	}
	s.lockConn = conn
	return conn, nil
}

func (s *CollabStore) lostLocked() {
	if s.lockConn != nil {
		_ = s.lockConn.Close()
		s.lockConn = nil
	}
	hadLocks := len(s.held) > 0
	s.held = map[string]int{}
	if hadLocks && s.OnLost != nil {
		go s.OnLost()
	}
}

func (s *CollabStore) ClaimRoom(ctx context.Context, roomID string) (bool, error) {
	s.lockMu.Lock()
	defer s.lockMu.Unlock()
	conn, err := s.lockConnLocked(ctx)
	if err != nil {
		return false, err
	}
	var got bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, collabLockKey(roomID)).Scan(&got); err != nil {
		s.lostLocked()
		return false, err
	}
	if got {
		s.held[roomID]++
	}
	return got, nil
}

func (s *CollabStore) ReleaseRoom(ctx context.Context, roomID string) error {
	s.lockMu.Lock()
	defer s.lockMu.Unlock()
	if s.held[roomID] == 0 || s.lockConn == nil {
		return nil
	}
	var released bool
	if err := s.lockConn.QueryRowContext(ctx, `SELECT pg_advisory_unlock($1)`, collabLockKey(roomID)).Scan(&released); err != nil {
		s.lostLocked()
		return err
	}
	if s.held[roomID]--; s.held[roomID] <= 0 {
		delete(s.held, roomID)
	}
	return nil
}

func (s *CollabStore) Close() {
	s.lockMu.Lock()
	defer s.lockMu.Unlock()
	if s.lockConn != nil {
		_ = s.lockConn.Close()
		s.lockConn = nil
	}
	s.held = map[string]int{}
}

func (s *CollabStore) SetRoomOwner(ctx context.Context, roomID, url string) error {
	return s.db.SpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE collab_rooms SET owner_url=$2,owner_at=NOW() WHERE id=$1`, roomID, url)
		return err
	})
}

func collabChecksum(state []byte) string {
	sum := sha256.Sum256(state)
	return hex.EncodeToString(sum[:])
}

func (s *CollabStore) LoadDocument(ctx context.Context, roomID string) (collab.Document, error) {
	var doc collab.Document
	err := s.db.SpaceTx(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT state,checksum,saved_at FROM collab_documents WHERE room_id=$1`, roomID).
			Scan(&doc.State, &doc.Checksum, &doc.SavedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return collab.ErrNoDocument
		}
		return err
	})
	if err != nil {
		return collab.Document{}, err
	}
	if collabChecksum(doc.State) != doc.Checksum {
		return collab.Document{}, collab.ErrNoDocument
	}
	return doc, nil
}

func (s *CollabStore) SaveDocument(ctx context.Context, roomID string, state []byte, checksum string) error {
	if len(state) > collab.MaxDocumentBytes {
		return collab.ErrTooLarge
	}
	return s.db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO collab_documents(room_id,state,byte_size,checksum,saved_at)
			VALUES($1,$2,$3,$4,NOW())
			ON CONFLICT (room_id) DO UPDATE SET state=EXCLUDED.state,byte_size=EXCLUDED.byte_size,
				checksum=EXCLUDED.checksum,saved_at=EXCLUDED.saved_at`, roomID, state, len(state), checksum); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE collab_rooms SET updated_at=NOW() WHERE id=$1`, roomID)
		return err
	})
}

func (s *CollabStore) DeleteDocument(ctx context.Context, roomID string) error {
	return s.db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM collab_documents WHERE room_id=$1`, roomID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE collab_rooms SET generation=generation+1,updated_at=NOW() WHERE id=$1`, roomID)
		return err
	})
}

func (s *CollabStore) LoadFileState(ctx context.Context, roomID string) (collabFileState, bool, error) {
	var raw []byte
	err := s.db.SpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT state FROM collab_file_state WHERE room_id=$1`, roomID).Scan(&raw)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return collabFileState{}, false, nil
	}
	if err != nil {
		return collabFileState{}, false, err
	}
	var state collabFileState
	if json.Unmarshal(raw, &state) != nil {
		return collabFileState{}, false, nil
	}
	return state, true, nil
}

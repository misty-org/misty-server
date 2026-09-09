package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	cap "github.com/kannachi323/misty/server/internal/capabilities"
)

type RoutineDraftRecord struct {
	RoutineID      string          `json:"routineId"`
	Version        int             `json:"version"`
	CurrentVersion int             `json:"currentVersion"`
	State          string          `json:"state"`
	Definition     json.RawMessage `json:"definition"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
}
type RoutineDraftSummary struct {
	RoutineID   string    `json:"routineId"`
	Version     int       `json:"version"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	State       string    `json:"state"`
	UpdatedAt   time.Time `json:"updatedAt"`
}
type RoutineDraftPage struct {
	Routines   []RoutineDraftSummary `json:"routines"`
	NextCursor string                `json:"nextCursor,omitempty"`
}

// SaveRoutineDraft is a trusted authoring control. It stores inert definitions,
// never run intents or grants. Apps need a separately scoped authoring path.
func (db *Database) SaveRoutineDraft(ctx context.Context, userID, routineID string, expectedVersion int, raw json.RawMessage) (*RoutineDraftRecord, error) {
	if userID == "" || AppAuthorityFromContext(ctx) != nil {
		return nil, ErrAppRuntimeForbidden
	}
	if !cap.ValidID(routineID) || expectedVersion < 0 || expectedVersion >= 2147483647 {
		return nil, cap.ErrInvalid
	}
	definition, normalized, err := cap.ParseRoutineDefinition(raw)
	if err != nil {
		return nil, err
	}
	var result *RoutineDraftRecord
	err = db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "routine:draft:"+userID+":"+routineID); err != nil {
			return err
		}
		if err := sdkTargetSpaceAccessTx(ctx, tx, userID, definition.SpaceID); err != nil {
			return err
		}
		var current int
		var spaceID string
		err := tx.QueryRowContext(ctx, `SELECT current_version,COALESCE(space_id,'') FROM misty_routines WHERE user_id=$1 AND id=$2 FOR UPDATE`, userID, routineID).Scan(&current, &spaceID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if current != 0 && spaceID != definition.SpaceID {
			return ErrSpaceConflict
		}
		if current == expectedVersion+1 {
			previous, err := routineDraftTx(ctx, tx, userID, routineID, current)
			if err != nil {
				return err
			}
			if !cap.EqualJSON(previous.Definition, normalized) {
				return ErrSpaceConflict
			}
			result = previous
			return nil
		}
		if current != expectedVersion {
			return ErrSpaceConflict
		}
		next := current + 1
		if current == 0 {
			_, err = tx.ExecContext(ctx, `INSERT INTO misty_routines(user_id,id,space_id,current_version) VALUES($1,$2,NULLIF($3,''),$4)`, userID, routineID, definition.SpaceID, next)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE misty_routines SET current_version=$3,updated_at=NOW() WHERE user_id=$1 AND id=$2`, userID, routineID, next)
		}
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO misty_routine_versions(user_id,routine_id,version,definition) VALUES($1,$2,$3,$4)`, userID, routineID, next, normalized); err != nil {
			return err
		}
		result, err = routineDraftTx(ctx, tx, userID, routineID, next)
		return err
	})
	return result, err
}
func routineDraftTx(ctx context.Context, tx *sql.Tx, userID, routineID string, version int) (*RoutineDraftRecord, error) {
	var result RoutineDraftRecord
	var spaceID string
	err := tx.QueryRowContext(ctx, `SELECT r.id,v.version,r.current_version,COALESCE(r.space_id,''),v.definition,v.created_at,r.updated_at FROM misty_routines r JOIN misty_routine_versions v ON v.user_id=r.user_id AND v.routine_id=r.id AND v.version=CASE WHEN $3=0 THEN r.current_version ELSE $3 END WHERE r.user_id=$1 AND r.id=$2`, userID, routineID, version).Scan(&result.RoutineID, &result.Version, &result.CurrentVersion, &spaceID, &result.Definition, &result.CreatedAt, &result.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := sdkTargetSpaceAccessTx(ctx, tx, userID, spaceID); err != nil {
		return nil, err
	}
	result.State = "draft"
	return &result, nil
}
func (db *Database) RoutineDraft(ctx context.Context, userID, routineID string, version int) (*RoutineDraftRecord, error) {
	if userID == "" || AppAuthorityFromContext(ctx) != nil {
		return nil, ErrAppRuntimeForbidden
	}
	if !cap.ValidID(routineID) || version < 0 || version > 2147483647 {
		return nil, cap.ErrInvalid
	}
	var result *RoutineDraftRecord
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		var err error
		result, err = routineDraftTx(ctx, tx, userID, routineID, version)
		return err
	})
	return result, err
}

// Explicit Space pagination avoids loading private definitions just to display
// the collection, and rechecks current membership on every page.
func (db *Database) RoutineDrafts(ctx context.Context, userID, spaceID, after string, limit int) (*RoutineDraftPage, error) {
	if userID == "" || AppAuthorityFromContext(ctx) != nil {
		return nil, ErrAppRuntimeForbidden
	}
	if after != "" && !cap.ValidID(after) || limit < 1 || limit > 100 || len(spaceID) > 256 {
		return nil, cap.ErrInvalid
	}
	page := &RoutineDraftPage{Routines: []RoutineDraftSummary{}}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		if err := sdkTargetSpaceAccessTx(ctx, tx, userID, spaceID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT r.id,r.current_version,v.definition->>'name',v.definition->>'description',r.updated_at FROM misty_routines r JOIN misty_routine_versions v ON v.user_id=r.user_id AND v.routine_id=r.id AND v.version=r.current_version WHERE r.user_id=$1 AND COALESCE(r.space_id,'')=$2 AND ($3='' OR r.id>NULLIF($3,'')::uuid) ORDER BY r.id LIMIT $4`, userID, spaceID, after, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item RoutineDraftSummary
			if err := rows.Scan(&item.RoutineID, &item.Version, &item.Name, &item.Description, &item.UpdatedAt); err != nil {
				return err
			}
			if len(page.Routines) == limit {
				page.NextCursor = page.Routines[len(page.Routines)-1].RoutineID
				break
			}
			item.State = "draft"
			page.Routines = append(page.Routines, item)
		}
		return rows.Err()
	})
	return page, err
}

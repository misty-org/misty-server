package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/misty-org/misty-server/internal/platform/config"
)

var (
	ErrSpaceAgentLimit   = errors.New("space agent limit reached")
	ErrSpaceUploadLimit  = errors.New("space upload limit reached")
	ErrSpaceStorageLimit = errors.New("space storage limit reached")
)

type SpaceLevelLimits struct {
	Members      *int64 `json:"members"`
	StorageBytes *int64 `json:"storage_bytes"`
	UploadBytes  *int64 `json:"upload_bytes"`
	Agents       *int64 `json:"agents"`
	CustomIcon   bool   `json:"custom_icon"`
	InviteLink   bool   `json:"invite_link"`
	HistoryDays  *int64 `json:"history_days"`
}

type SpaceLevelConfig struct {
	Thresholds           []int64            `json:"thresholds"`
	StorageBytesPerBoost int64              `json:"storage_bytes_per_boost"`
	Levels               []SpaceLevelLimits `json:"levels"`
}

type SpaceLevel struct {
	Level      int              `json:"level"`
	Boosts     int64            `json:"boosts"`
	NextLevel  int64            `json:"next_level_boosts"`
	Thresholds []int64          `json:"thresholds"`
	Limits     SpaceLevelLimits `json:"limits"`
}

var spaceLevelCache struct {
	sync.Mutex
	loaded bool
	raw    string
	config *SpaceLevelConfig
	err    error
}

func SpaceLevelsConfigured() (*SpaceLevelConfig, error) {
	raw := strings.TrimSpace(config.Getenv("MISTY_SPACE_LEVELS"))
	spaceLevelCache.Lock()
	defer spaceLevelCache.Unlock()
	if spaceLevelCache.loaded && raw == spaceLevelCache.raw {
		return spaceLevelCache.config, spaceLevelCache.err
	}
	spaceLevelCache.loaded, spaceLevelCache.raw, spaceLevelCache.config, spaceLevelCache.err = true, raw, nil, nil
	if raw == "" {
		return nil, nil
	}
	var parsed SpaceLevelConfig
	err := json.Unmarshal([]byte(raw), &parsed)
	if err == nil {
		err = parsed.validate()
	}
	if err != nil {
		spaceLevelCache.err = fmt.Errorf("MISTY_SPACE_LEVELS: %w", err)
		return nil, spaceLevelCache.err
	}
	spaceLevelCache.config = &parsed
	return &parsed, nil
}

func (c SpaceLevelConfig) validate() error {
	if len(c.Levels) != len(c.Thresholds)+1 {
		return errors.New("levels must be one more than thresholds: the base level, then one per threshold")
	}
	last := int64(0)
	for _, threshold := range c.Thresholds {
		if threshold <= last {
			return errors.New("thresholds must rise and start above 0")
		}
		last = threshold
	}
	if c.StorageBytesPerBoost < 0 {
		return errors.New("storage_bytes_per_boost must be at least 0")
	}
	for _, level := range c.Levels {
		if level.HistoryDays != nil && *level.HistoryDays < 1 {
			return errors.New("history_days must be at least 1, or null for unlimited")
		}
	}
	return nil
}

func (c SpaceLevelConfig) levelFor(boosts int64) SpaceLevel {
	level := 0
	for i, threshold := range c.Thresholds {
		if boosts >= threshold {
			level = i + 1
		}
	}
	out := SpaceLevel{Level: level, Boosts: boosts, Thresholds: c.Thresholds, Limits: c.Levels[level]}
	if level < len(c.Thresholds) {
		out.NextLevel = c.Thresholds[level]
	}
	if out.Limits.StorageBytes != nil && level > 0 {
		storage := *out.Limits.StorageBytes + (boosts-c.Thresholds[level-1])*c.StorageBytesPerBoost
		out.Limits.StorageBytes = &storage
	}
	return out
}

func spaceBoostsTx(ctx context.Context, tx *sql.Tx, spaceID string) (int64, error) {
	var boosts int64
	err := asServiceTx(ctx, tx, func() error {
		return tx.QueryRowContext(ctx, `SELECT COALESCE(sum(b.boosts),0) FROM space_boosts b
			JOIN space_members m ON m.space_id=b.space_id AND m.user_id=b.user_id WHERE b.space_id=$1`, spaceID).Scan(&boosts)
	})
	return boosts, err
}

func spaceLevelTx(ctx context.Context, tx *sql.Tx, spaceID string) (*SpaceLevel, error) {
	levels, err := SpaceLevelsConfigured()
	if err != nil || levels == nil {
		return nil, err
	}
	boosts, err := spaceBoostsTx(ctx, tx, spaceID)
	if err != nil {
		return nil, err
	}
	level := levels.levelFor(boosts)
	return &level, nil
}

func boostedSpacesThreshold() int64 {
	levels, err := SpaceLevelsConfigured()
	if err != nil || levels == nil || len(levels.Thresholds) == 0 {
		return 1 << 62
	}
	return levels.Thresholds[0]
}

type SpaceBooster struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Boosts   int64  `json:"boosts"`
}

type SpaceBoostState struct {
	SpaceID          string         `json:"space_id"`
	Enabled          bool           `json:"enabled"`
	Level            *SpaceLevel    `json:"level,omitempty"`
	Boosters         []SpaceBooster `json:"boosters"`
	StorageUsedBytes int64          `json:"storage_used_bytes"`
}

func (db *Database) SpaceBoosts(ctx context.Context, userID, spaceID string) (*SpaceBoostState, error) {
	out := &SpaceBoostState{SpaceID: spaceID, Boosters: []SpaceBooster{}}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		level, err := spaceLevelTx(ctx, tx, spaceID)
		if err != nil {
			return err
		}
		out.Enabled, out.Level = level != nil, level
		return asServiceTx(ctx, tx, func() error {
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT used_bytes+reserved_bytes FROM space_storage_usage WHERE space_id=$1),0)`, spaceID).Scan(&out.StorageUsedBytes); err != nil {
				return err
			}
			rows, err := tx.QueryContext(ctx, `SELECT b.user_id,u.username,b.boosts FROM space_boosts b
				JOIN space_members m ON m.space_id=b.space_id AND m.user_id=b.user_id
				JOIN users u ON u.id=b.user_id WHERE b.space_id=$1 ORDER BY b.boosts DESC,b.updated_at`, spaceID)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var booster SpaceBooster
				if err := rows.Scan(&booster.UserID, &booster.Username, &booster.Boosts); err != nil {
					return err
				}
				out.Boosters = append(out.Boosters, booster)
			}
			return rows.Err()
		})
	})
	return out, err
}

func (db *Database) SetSpaceBoosts(ctx context.Context, spaceID, userID string, boosts int64) error {
	if boosts < 0 || boosts > 1000 {
		return ErrSpaceInvalid
	}
	return db.serviceTx(ctx, func(tx *sql.Tx) error {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM spaces WHERE id=$1 AND lifecycle_state='active')`, spaceID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrSpaceNotFound
		}
		if err := requireActiveAccountTx(ctx, tx, userID); err != nil {
			return err
		}
		if boosts == 0 {
			_, err := tx.ExecContext(ctx, `DELETE FROM space_boosts WHERE space_id=$1 AND user_id=$2`, spaceID, userID)
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO space_boosts(space_id,user_id,boosts) VALUES($1,$2,$3)
			ON CONFLICT(space_id,user_id) DO UPDATE SET boosts=EXCLUDED.boosts,updated_at=now()`, spaceID, userID, boosts)
		return err
	})
}

type OperatorSpace struct {
	ID      string      `json:"id"`
	Name    string      `json:"name"`
	Members []string    `json:"member_ids"`
	Level   *SpaceLevel `json:"level,omitempty"`
}

func (db *Database) OperatorSpaceByID(ctx context.Context, spaceID string) (*OperatorSpace, error) {
	out := &OperatorSpace{ID: spaceID, Members: []string{}}
	err := db.serviceTx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT name FROM spaces WHERE id=$1 AND lifecycle_state='active'`, spaceID).Scan(&out.Name); errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceNotFound
		} else if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT user_id FROM space_members WHERE space_id=$1 ORDER BY joined_at`, spaceID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			out.Members = append(out.Members, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		out.Level, err = spaceLevelTx(ctx, tx, spaceID)
		return err
	})
	return out, err
}

func requireSpaceAgentRoomTx(ctx context.Context, tx *sql.Tx, spaceID, agentID, ownerID string) error {
	level, err := spaceLevelTx(ctx, tx, spaceID)
	if err != nil || level == nil || level.Limits.Agents == nil {
		return err
	}
	var agents int64
	var already bool
	if err := asServiceTx(ctx, tx, func() error {
		return tx.QueryRowContext(ctx, `SELECT count(*),COALESCE(bool_or(agent_id=$2 AND owner_user_id=$3),false) FROM space_agents WHERE space_id=$1`,
			spaceID, agentID, ownerID).Scan(&agents, &already)
	}); err != nil {
		return err
	}
	if !already && agents >= *level.Limits.Agents {
		return ErrSpaceAgentLimit
	}
	return nil
}

func requireSpaceMembersRoomTx(ctx context.Context, tx *sql.Tx, spaceID string, here int64) error {
	level, err := spaceLevelTx(ctx, tx, spaceID)
	if err != nil || level == nil || level.Limits.Members == nil {
		return err
	}
	if here >= *level.Limits.Members {
		return ErrSpacePeopleLimit
	}
	return nil
}

func requireSpaceUploadSizeTx(ctx context.Context, tx *sql.Tx, userID, spaceID string, byteSize int64) error {
	level, err := spaceLevelTx(ctx, tx, spaceID)
	if err != nil || level == nil || level.Limits.UploadBytes == nil || byteSize <= *level.Limits.UploadBytes {
		return err
	}
	var personal *int64
	if err := asServiceTx(ctx, tx, func() error {
		limits, _, err := accountControlsTx(ctx, tx, userID)
		personal = limits.UploadBytes
		return err
	}); err != nil {
		return err
	}
	if personal != nil && byteSize <= *personal {
		return nil
	}
	return ErrSpaceUploadLimit
}

func RequireStorageRoomTx(ctx context.Context, tx *sql.Tx, userID, spaceID string, adding int64) error {
	if adding <= 0 {
		return nil
	}
	level, err := spaceLevelTx(ctx, tx, spaceID)
	if err != nil {
		return err
	}
	if level == nil || level.Level == 0 {
		return RequireAccountStorageRoomTx(ctx, tx, userID, adding)
	}
	if level.Limits.StorageBytes == nil {
		return nil
	}
	return asServiceTx(ctx, tx, func() error {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "space-storage:"+spaceID); err != nil {
			return err
		}
		var used int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT used_bytes+reserved_bytes FROM space_storage_usage WHERE space_id=$1),0)`, spaceID).Scan(&used); err != nil {
			return err
		}
		if used+adding > *level.Limits.StorageBytes {
			return ErrSpaceStorageLimit
		}
		return nil
	})
}

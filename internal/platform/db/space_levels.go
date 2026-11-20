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

// Space levels: what a Space may do, raised by the Boosts its members give it. Misty Cloud
// sells Boosts; a server that doesn't configure levels (MISTY_SPACE_LEVELS unset, every
// self-hosted server by default) has none, and every Space there is unlimited.
//
// MISTY_SPACE_LEVELS is JSON: the Boosts each level needs, and each level's limits, base
// level first. A null or missing limit is unlimited.
//
//	{"thresholds":[2,7,14],"storage_bytes_per_boost":5000000000,"levels":[
//	  {"members":10,"upload_bytes":26214400,"agents":3},
//	  {"members":25,"storage_bytes":25000000000,"upload_bytes":104857600,"agents":10,"custom_icon":true,"history_days":30},
//	  ...]}
//
// Only Boosts from people still in the Space count. A Space whose Boosts lapse drops a level
// and keeps everything; what's over a limit stays, and nothing new that needs room goes in.

var (
	// ErrSpaceAppLimit refuses one more installed app past a fixed bound.
	ErrSpaceAppLimit = errors.New("space app limit reached")
	// ErrSpaceAgentLimit refuses one more agent in a Space at its level's agent limit.
	ErrSpaceAgentLimit = errors.New("space agent limit reached")
	// ErrSpaceUploadLimit refuses a file larger than the Space's level allows.
	ErrSpaceUploadLimit = errors.New("space upload limit reached")
	// ErrSpaceStorageLimit refuses adding to a boosted Space whose shared storage is full.
	ErrSpaceStorageLimit = errors.New("space storage limit reached")
)

// SpaceLevelLimits are one level's limits; nil is unlimited.
type SpaceLevelLimits struct {
	Members      *int64 `json:"members"`
	StorageBytes *int64 `json:"storage_bytes"`
	UploadBytes  *int64 `json:"upload_bytes"`
	Agents       *int64 `json:"agents"`
	CustomIcon   bool   `json:"custom_icon"`
	InviteLink   bool   `json:"invite_link"`
	// HistoryDays is how long a doc's earlier versions are kept.
	HistoryDays *int64 `json:"history_days"`
}

// SpaceLevelConfig is MISTY_SPACE_LEVELS, read.
type SpaceLevelConfig struct {
	Thresholds           []int64            `json:"thresholds"`
	StorageBytesPerBoost int64              `json:"storage_bytes_per_boost"`
	Levels               []SpaceLevelLimits `json:"levels"`
}

// SpaceLevel is where a Space stands: its level (0 is the base), the Boosts it has, the Boosts
// the next level needs (0 at the top), and its limits. StorageBytes includes the bonus each
// Boost past its level's threshold adds.
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

// SpaceLevelsConfigured reads MISTY_SPACE_LEVELS: nil when unset, so Spaces have no levels.
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

// levelFor is the level a number of Boosts reaches.
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

// spaceBoostsTx counts the Boosts of the Space's current members.
func spaceBoostsTx(ctx context.Context, tx *sql.Tx, spaceID string) (int64, error) {
	var boosts int64
	err := asServiceTx(ctx, tx, func() error {
		return tx.QueryRowContext(ctx, `SELECT COALESCE(sum(b.boosts),0) FROM space_boosts b
			JOIN space_members m ON m.space_id=b.space_id AND m.user_id=b.user_id WHERE b.space_id=$1`, spaceID).Scan(&boosts)
	})
	return boosts, err
}

// spaceLevelTx is the Space's level, or nil when this server has no levels.
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

// boostedSpacesThreshold is the Boosts a Space needs before its files use its own storage
// instead of its members': the first level's. With no levels nothing reaches it.
func boostedSpacesThreshold() int64 {
	levels, err := SpaceLevelsConfigured()
	if err != nil || levels == nil || len(levels.Thresholds) == 0 {
		return 1 << 62
	}
	return levels.Thresholds[0]
}

// SpaceBooster is one member's Boosts on a Space.
type SpaceBooster struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Boosts   int64  `json:"boosts"`
}

// SpaceBoostState is a Space's level and who boosts it, for its members.
type SpaceBoostState struct {
	SpaceID  string         `json:"space_id"`
	Enabled  bool           `json:"enabled"`
	Level    *SpaceLevel    `json:"level,omitempty"`
	Boosters []SpaceBooster `json:"boosters"`
	// StorageUsedBytes is what the Space stores, which a boosted Space counts against its own storage.
	StorageUsedBytes int64 `json:"storage_used_bytes"`
}

// SpaceBoosts answers a member with the Space's level and its boosters.
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

// SetSpaceBoosts records how many Boosts an account gives a Space; 0 takes them away. Billing
// calls it through the operator API. The Space must exist; the account needn't be a member
// yet, but its Boosts only count while it is one.
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

// OperatorSpace is a Space as the operator API shows it: enough for billing to check that
// someone buying Boosts for it is a member.
type OperatorSpace struct {
	ID      string      `json:"id"`
	Name    string      `json:"name"`
	Members []string    `json:"member_ids"`
	Level   *SpaceLevel `json:"level,omitempty"`
}

// OperatorSpaceByID is one active Space with its member IDs and level.
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

// requireSpaceAgentRoomTx refuses one more agent in a Space at its level's agent limit.
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

// requireSpaceMembersRoomTx refuses one more person in a Space at its level's member limit;
// here counts the members and open invitations already in it.
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

// requireSpaceUploadSizeTx refuses a file larger than the Space's level allows, unless the
// uploader's own plan allows larger (account_controls.upload_bytes, Misty+'s).
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

// RequireStorageRoomTx refuses adding bytes to a Space past what may hold them. A boosted Space
// (at level 1 or above) stores its files in its own storage, so they count against the Space's
// limit and not the uploader's; any other Space's files count against the uploader's account.
// Callers hold the account's storage lock (prepareStorageReservationTx).
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

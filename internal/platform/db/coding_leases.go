package db

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
)

// CodingLease is a budget a delegated coding run may spend.
type CodingLease struct {
	ID           string    `json:"id"`
	InvocationID string    `json:"invocation_id"`
	DeviceID     string    `json:"device_id"`
	Engine       string    `json:"engine"`
	Model        string    `json:"model"`
	TokenBudget  int64     `json:"token_budget"`
	UsedTokens   int64     `json:"used_tokens"`
	ExpiresAt    time.Time `json:"expires_at"`
	// What the tool reported, by token class, for settling the lease once.
	Usage CodingLeaseUsage `json:"-"`
}

// CodingLeaseUsage is one usage report: the raw token classes and their
// weight against the weekly allowance.
type CodingLeaseUsage struct {
	Input, Output, CacheWrite, CacheRead, Weighted int64
}

const codingLeaseColumns = `id,invocation_id,device_id,engine,model,token_budget,used_tokens,expires_at,
	input_tokens,output_tokens,cache_write_tokens,cache_read_tokens`

func scanCodingLease(row scanner, out *CodingLease) error {
	return row.Scan(&out.ID, &out.InvocationID, &out.DeviceID, &out.Engine, &out.Model, &out.TokenBudget, &out.UsedTokens, &out.ExpiresAt,
		&out.Usage.Input, &out.Usage.Output, &out.Usage.CacheWrite, &out.Usage.CacheRead)
}

func hashLeaseToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// WeeklyCodingUsed is the universal tokens the person's coding used since the
// start of this week (UTC).
func (db *Database) WeeklyCodingUsed(ctx context.Context, user string) (int64, error) {
	var used int64
	err := db.WithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(used_tokens),0) FROM coding_leases WHERE user_id=$1 AND created_at>=date_trunc('week', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'`, user).Scan(&used)
	})
	return used, err
}

// IssueCodingLease records a new lease and returns it with its token, shown once.
func (db *Database) IssueCodingLease(ctx context.Context, user string, lease CodingLease) (*CodingLease, string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, "", err
	}
	token := hex.EncodeToString(raw)
	out := &CodingLease{}
	err := db.WithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		return scanCodingLease(tx.QueryRowContext(ctx, `INSERT INTO coding_leases(id,user_id,invocation_id,device_id,engine,model,token_hash,token_budget,expires_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING `+codingLeaseColumns,
			"lease_"+uuid.NewString(), user, lease.InvocationID, lease.DeviceID, lease.Engine, lease.Model, hashLeaseToken(token), lease.TokenBudget, lease.ExpiresAt), out)
	})
	return out, token, err
}

// CodingLeaseByToken is the lease a device presents, when its token matches.
func (db *Database) CodingLeaseByToken(ctx context.Context, user, invocationID, id, token string) (*CodingLease, error) {
	out := &CodingLease{}
	err := db.WithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		return scanCodingLease(tx.QueryRowContext(ctx, `SELECT `+codingLeaseColumns+` FROM coding_leases WHERE id=$1 AND user_id=$2 AND invocation_id=$3 AND token_hash=$4`,
			id, user, invocationID, hashLeaseToken(token)), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceForbidden
	}
	return out, err
}

// AddCodingLeaseUsage records what the tool used under a lease.
func (db *Database) AddCodingLeaseUsage(ctx context.Context, user, id string, usage CodingLeaseUsage) error {
	if usage.Input < 0 || usage.Output < 0 || usage.CacheWrite < 0 || usage.CacheRead < 0 || usage.Weighted < 0 {
		return ErrSpaceInvalid
	}
	return db.WithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE coding_leases SET used_tokens=used_tokens+$3,input_tokens=input_tokens+$4,output_tokens=output_tokens+$5,
			cache_write_tokens=cache_write_tokens+$6,cache_read_tokens=cache_read_tokens+$7 WHERE id=$1 AND user_id=$2`,
			id, user, usage.Weighted, usage.Input, usage.Output, usage.CacheWrite, usage.CacheRead)
		return err
	})
}

// UnsettledCodingLeases are a run's leases whose usage is not yet settled.
func (db *Database) UnsettledCodingLeases(ctx context.Context, user, invocationID string) ([]CodingLease, error) {
	leases := []CodingLease{}
	err := db.WithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT `+codingLeaseColumns+` FROM coding_leases
			WHERE user_id=$1 AND invocation_id=$2 AND settled_at IS NULL ORDER BY created_at`, user, invocationID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var lease CodingLease
			if err := scanCodingLease(rows, &lease); err != nil {
				return err
			}
			leases = append(leases, lease)
		}
		return rows.Err()
	})
	return leases, err
}

// MarkCodingLeaseSettled records that a lease's usage reached billing.
func (db *Database) MarkCodingLeaseSettled(ctx context.Context, user, id string) error {
	return db.WithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE coding_leases SET settled_at=now() WHERE id=$1 AND user_id=$2 AND settled_at IS NULL`, id, user)
		return err
	})
}

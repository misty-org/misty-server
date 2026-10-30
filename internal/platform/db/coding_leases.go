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

// CodingLease lets a device report a delegated coding run's turn.
type CodingLease struct {
	ID           string    `json:"id"`
	InvocationID string    `json:"invocation_id"`
	DeviceID     string    `json:"device_id"`
	Engine       string    `json:"engine"`
	Model        string    `json:"model"`
	TokenBudget  int64     `json:"token_budget"`
	ExpiresAt    time.Time `json:"expires_at"`
}

const codingLeaseColumns = `id,invocation_id,device_id,engine,model,token_budget,expires_at`

func scanCodingLease(row scanner, out *CodingLease) error {
	return row.Scan(&out.ID, &out.InvocationID, &out.DeviceID, &out.Engine, &out.Model, &out.TokenBudget, &out.ExpiresAt)
}

func hashLeaseToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
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

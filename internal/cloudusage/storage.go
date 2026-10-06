// Package cloudusage reports native cloud measurements through billing's adapter.
// It owns no plan limits, conversions, percentages, or quota arithmetic.
package cloudusage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/kannachi323/misty/server/internal/billingadapter"
)

type Storage struct {
	UsedBytes      int64   `json:"used_bytes"`
	ReservedBytes  int64   `json:"reserved_bytes"`
	LimitBytes     int64   `json:"limit_bytes"`
	RemainingBytes int64   `json:"remaining_bytes"`
	ApprovedBytes  int64   `json:"approved_bytes"`
	OverQuota      bool    `json:"over_quota"`
	PercentageUsed float64 `json:"percentage_used"`
	PolicyVersion  string  `json:"policy_version"`
}

func Lock(ctx context.Context, tx *sql.Tx, user string) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "storage-personal:"+user)
	return err
}

// Facts counts logical retained payloads, not physical replicas, indexes, or
// operational logs. The native ledgers remain authoritative across rollback.
func Facts(ctx context.Context, tx *sql.Tx, user string) (map[string]int64, error) {
	// Sync and ordinary content stores use different RLS contexts. This internal
	// inventory must observe the authenticated account across all its Spaces.
	var previous sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT current_setting('app.rls_mode',true)`).Scan(&previous); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `SELECT set_config('app.rls_mode','service',true)`); err != nil {
		return nil, err
	}
	defer tx.ExecContext(ctx, `SELECT set_config('app.rls_mode',$1,true)`, previous.String)
	var objects, reserved, database, sync int64
	err := tx.QueryRowContext(ctx, `SELECT
 COALESCE((SELECT sum(logical_bytes) FROM space_storage_contributions WHERE user_id=$1 AND state IN ('active','recovery')),0),
 COALESCE((SELECT sum(reserved_bytes) FROM space_upload_reservations WHERE user_id=$1 AND state='active'),0)+COALESCE((SELECT sum(reserved_bytes) FROM space_rendition_reservations WHERE user_id=$1 AND state='active'),0),
 COALESCE((SELECT sum(octet_length(content::text)) FROM space_messages WHERE sender_user_id=$1),0)+
 COALESCE((SELECT sum(octet_length(title_projection)+octet_length(markdown_projection)+octet_length(plain_text_projection)+octet_length(shared_tags::text)) FROM space_notes WHERE creator_user_id=$1),0),
 COALESCE((SELECT sum(octet_length(x.ciphertext)) FROM browser_sync_records x JOIN browser_sync_vaults v USING(vault_id) WHERE v.user_id=$1),0)+
 COALESCE((SELECT sum(octet_length(x.ciphertext)) FROM browser_sync_nodes x JOIN browser_sync_vaults v USING(vault_id) WHERE v.user_id=$1),0)+
 COALESCE((SELECT sum(octet_length(x.ciphertext)) FROM browser_sync_slots x JOIN browser_sync_vaults v USING(vault_id) WHERE v.user_id=$1),0)+
 COALESCE((SELECT sum(octet_length(x.ciphertext)) FROM browser_sync_blobs x JOIN browser_sync_vaults v USING(vault_id) WHERE v.user_id=$1),0)+
 COALESCE((SELECT sum(octet_length(x.manifest::text)) FROM browser_sync_changes x JOIN browser_sync_vaults v USING(vault_id) WHERE v.user_id=$1),0)+
 COALESCE((SELECT sum(octet_length(x.envelope::text)) FROM browser_sync_events x JOIN browser_sync_vaults v USING(vault_id) WHERE v.user_id=$1),0)`, user).Scan(&objects, &reserved, &database, &sync)
	return map[string]int64{"object_bytes": objects, "reserved_bytes": reserved, "database_bytes": database, "sync_bytes": sync}, err
}
func Check(ctx context.Context, tx *sql.Tx, adapter billingadapter.Adapter, user, operation string, request map[string]int64) (Storage, error) {
	units, err := Facts(ctx, tx, user)
	if err != nil {
		return Storage{}, err
	}
	for k, v := range request {
		units[k] = v
	}
	// A disabled development adapter grants work without fabricating a paid plan.
	if !adapter.Enabled() {
		return Storage{ApprovedBytes: request["requested_bytes"]}, nil
	}
	result, err := adapter.Do(ctx, "check", billingadapter.Request{Version: 1, AccountID: user, Operation: operation, OperationID: "cloud-storage", Key: "cloud-storage", Usage: billingadapter.Usage{Units: units}})
	if err != nil && !errors.Is(err, billingadapter.ErrDenied) {
		return Storage{}, err
	}
	var out Storage
	if json.Unmarshal(result.Summary, &out) != nil {
		return out, billingadapter.ErrUnavailable
	}
	return out, err
}

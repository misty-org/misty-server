package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/google/uuid"
)

// RefundHostedAIReservation restores quota for a settled provider call that
// Misty could not use because of an internal policy/capability mismatch. It is
// idempotent so retries and duplicate terminal events cannot credit twice.
func (db *Database) RefundHostedAIReservation(reservationID, idempotencyKey, reason string) (*HostedAIWallet, error) {
	if strings.TrimSpace(reservationID) == "" || strings.TrimSpace(idempotencyKey) == "" {
		return nil, errors.New("invalid hosted AI refund")
	}
	var wallet HostedAIWallet
	err := db.TestingWithRLSContext(context.Background(), TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		var userID, meter, provider, model string
		var charged int64
		if err := tx.QueryRowContext(context.Background(), `SELECT r.user_id,l.meter,l.provider,l.model,l.charged_microusd
			FROM hosted_ai_reservations r JOIN hosted_ai_usage_ledger l ON l.reservation_id=r.id AND l.source='consumption'
			WHERE r.id=$1 FOR UPDATE OF r,l`, reservationID).Scan(&userID, &meter, &provider, &model, &charged); err != nil {
			return err
		}
		if err := tx.QueryRowContext(context.Background(), `SELECT user_id,weekly_allowance_microusd,weekly_remaining_microusd,reserved_microusd,reset_at
			FROM hosted_ai_wallets WHERE user_id=$1 FOR UPDATE`, userID).Scan(
			&wallet.UserID, &wallet.WeeklyAllowanceMicrousd, &wallet.WeeklyRemainingMicrousd, &wallet.ReservedMicrousd, &wallet.ResetAt,
		); err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRowContext(context.Background(), `SELECT EXISTS(SELECT 1 FROM hosted_ai_usage_ledger WHERE idempotency_key=$1)`, idempotencyKey).Scan(&exists); err != nil || exists {
			return err
		}
		refunded := min(charged, wallet.WeeklyAllowanceMicrousd-wallet.WeeklyRemainingMicrousd)
		if refunded < 0 {
			refunded = 0
		}
		if _, err := tx.ExecContext(context.Background(), `UPDATE hosted_ai_wallets
			SET weekly_remaining_microusd=LEAST(weekly_allowance_microusd,weekly_remaining_microusd+$2),updated_at=NOW()
			WHERE user_id=$1`, userID, refunded); err != nil {
			return err
		}
		source := "internal_failure_refund"
		if value := strings.TrimSpace(reason); value != "" {
			source += ":" + value
		}
		if _, err := tx.ExecContext(context.Background(), `INSERT INTO hosted_ai_usage_ledger(
			id,user_id,reservation_id,source,meter,weekly_delta_microusd,provider,model,rate_card_version,idempotency_key
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, uuid.NewString(), userID, reservationID, source, meter, refunded, provider, model, HostedAIRateCardVersion, idempotencyKey); err != nil {
			return err
		}
		_, err := tx.ExecContext(context.Background(), `UPDATE hosted_ai_reservations SET status='refunded' WHERE id=$1`, reservationID)
		if err == nil {
			wallet.WeeklyRemainingMicrousd += refunded
		}
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("hosted AI reservation is not settled")
	}
	return &wallet, err
}

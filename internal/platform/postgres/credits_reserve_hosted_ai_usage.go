package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"

	"github.com/google/uuid"
)

func (db *Database) ReserveHostedAIUsage(userID string, tier Tier, meter, idempotencyKey string, amount int64, now time.Time) (*HostedAIReservation, *HostedAIWallet, error) {
	return db.reserveHostedAIUsage(userID, "", tier, meter, idempotencyKey, amount, now, false)
}

// ReserveHostedAIUsageForSpace atomically reserves from both the initiating
// member's global personal allowance and the owner-plan allowance of spaceID.
func (db *Database) ReserveHostedAIUsageForSpace(userID, spaceID string, tier Tier, meter, idempotencyKey string, amount int64, now time.Time) (*HostedAIReservation, *HostedAIWallet, error) {
	return db.reserveHostedAIUsage(userID, spaceID, tier, meter, idempotencyKey, amount, now, false)
}

// ReserveHostedAIUsageUpTo reserves the requested amount when possible and the
// entire remaining allowance otherwise. Agent completions use this because
// their reservation is a worst-case output estimate rather than a fixed-cost
// operation. Settling still charges actual usage, while reserving everything
// left prevents concurrent completions from overspending the account.
func (db *Database) ReserveHostedAIUsageUpTo(userID string, tier Tier, meter, idempotencyKey string, amount int64, now time.Time) (*HostedAIReservation, *HostedAIWallet, error) {
	return db.reserveHostedAIUsage(userID, "", tier, meter, idempotencyKey, amount, now, true)
}

func (db *Database) ReserveHostedAIUsageUpToForSpace(userID, spaceID string, tier Tier, meter, idempotencyKey string, amount int64, now time.Time) (*HostedAIReservation, *HostedAIWallet, error) {
	return db.reserveHostedAIUsage(userID, spaceID, tier, meter, idempotencyKey, amount, now, true)
}

func (db *Database) reserveHostedAIUsage(userID, spaceID string, tier Tier, meter, idempotencyKey string, amount int64, now time.Time, allowPartial bool) (*HostedAIReservation, *HostedAIWallet, error) {
	if amount <= 0 || strings.TrimSpace(idempotencyKey) == "" {
		return nil, nil, errors.New("invalid hosted AI reservation")
	}
	spaceID = strings.TrimSpace(spaceID)
	if _, err := db.GetOrCreateHostedAIWallet(userID, tier, now); err != nil {
		return nil, nil, err
	}
	reservation := &HostedAIReservation{ID: uuid.NewString(), UserID: userID, SpaceID: spaceID, ReservedMicrousd: amount, ReservedCredits: amount, Status: "reserved"}
	var wallet HostedAIWallet
	var spaceWallet SpaceHostedAIWallet
	reservedNow := false
	err := db.TestingWithRLSContext(context.Background(), TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		if spaceID != "" {
			var err error
			spaceWallet, err = refreshSpaceHostedAIWalletTx(context.Background(), tx, spaceID, now.UTC())
			if err != nil {
				return err
			}
			var member bool
			if err := tx.QueryRowContext(context.Background(), `SELECT EXISTS(SELECT 1 FROM space_members WHERE space_id=$1 AND user_id=$2)`, spaceID, userID).Scan(&member); err != nil {
				return err
			}
			if !member {
				return ErrSpaceForbidden
			}
		}
		if err := tx.QueryRowContext(context.Background(), `SELECT user_id,weekly_allowance_microusd,weekly_remaining_microusd,reserved_microusd,reset_at FROM hosted_ai_wallets WHERE user_id=$1 FOR UPDATE`, userID).
			Scan(&wallet.UserID, &wallet.WeeklyAllowanceMicrousd, &wallet.WeeklyRemainingMicrousd, &wallet.ReservedMicrousd, &wallet.ResetAt); err != nil {
			return err
		}
		var existingID, existingUserID, existingSpaceID, existingMeter, existingStatus string
		var existingAmount int64
		existingErr := tx.QueryRowContext(context.Background(), `SELECT id,user_id,COALESCE(space_id,''),meter,reserved_microusd,status FROM hosted_ai_reservations WHERE idempotency_key=$1 FOR UPDATE`, idempotencyKey).
			Scan(&existingID, &existingUserID, &existingSpaceID, &existingMeter, &existingAmount, &existingStatus)
		if existingErr == nil {
			amountMatches := existingAmount == amount || (allowPartial && existingAmount < amount)
			if existingUserID != userID || existingSpaceID != spaceID || existingMeter != meter || !amountMatches {
				return errors.New("hosted AI idempotency key reused with different reservation parameters")
			}
			reservation.ID, reservation.Status = existingID, existingStatus
			reservation.ReservedMicrousd, reservation.ReservedCredits = existingAmount, existingAmount
			if existingStatus != "released" {
				return nil
			}
			reserveAmount, limitErr := hostedAIReserveAmount(wallet, spaceWallet, amount, allowPartial, spaceID != "")
			if limitErr != nil {
				return limitErr
			}
			if _, err := tx.ExecContext(context.Background(), `UPDATE hosted_ai_reservations SET reserved_microusd=$2,status='reserved',created_at=NOW(),settled_at=NULL WHERE id=$1`, existingID, reserveAmount); err != nil {
				return err
			}
			reservation.Status = "reserved"
			reservation.ReservedMicrousd, reservation.ReservedCredits = reserveAmount, reserveAmount
			reservedNow = true
			return reserveHostedAIWalletsTx(tx, userID, spaceID, reserveAmount)
		}
		if !errors.Is(existingErr, sql.ErrNoRows) {
			return existingErr
		}
		reserveAmount, limitErr := hostedAIReserveAmount(wallet, spaceWallet, amount, allowPartial, spaceID != "")
		if limitErr != nil {
			return limitErr
		}
		if _, err := tx.ExecContext(context.Background(), `INSERT INTO hosted_ai_reservations(id,user_id,space_id,idempotency_key,meter,reserved_microusd) VALUES($1,$2,NULLIF($3,''),$4,$5,$6)`, reservation.ID, userID, spaceID, idempotencyKey, meter, reserveAmount); err != nil {
			return err
		}
		reservation.ReservedMicrousd, reservation.ReservedCredits = reserveAmount, reserveAmount
		reservedNow = true
		return reserveHostedAIWalletsTx(tx, userID, spaceID, reserveAmount)
	})
	if err != nil {
		return nil, &wallet, err
	}
	if reservedNow {
		wallet.ReservedMicrousd += reservation.ReservedMicrousd
	}
	return reservation, &wallet, nil
}

func hostedAIReserveAmount(personal HostedAIWallet, space SpaceHostedAIWallet, requested int64, allowPartial, hasSpace bool) (int64, error) {
	available, scope := personal.Available(), "personal"
	if hasSpace && space.Available() < available {
		available, scope = space.Available(), "space"
	}
	if available >= requested {
		return requested, nil
	}
	if allowPartial && available > 0 {
		return available, nil
	}
	return 0, HostedAILimitReachedError{Available: available, Required: requested, Scope: scope}
}

func reserveHostedAIWalletsTx(tx *sql.Tx, userID, spaceID string, amount int64) error {
	if _, err := tx.ExecContext(context.Background(), `UPDATE hosted_ai_wallets SET reserved_microusd=reserved_microusd+$2,updated_at=NOW() WHERE user_id=$1`, userID, amount); err != nil {
		return err
	}
	if spaceID != "" {
		_, err := tx.ExecContext(context.Background(), `UPDATE space_hosted_ai_wallets SET reserved_microusd=reserved_microusd+$2,updated_at=NOW() WHERE space_id=$1`, spaceID, amount)
		return err
	}
	return nil
}

func (db *Database) ReserveCredits(userID string, tier Tier, meter, idempotencyKey string, amount int64, now time.Time) (*CreditReservation, *CreditWallet, error) {
	return db.ReserveHostedAIUsage(userID, tier, meter, idempotencyKey, amount, now)
}

func (db *Database) ReleaseHostedAIReservation(reservationID string) error {
	return db.TestingWithRLSContext(context.Background(), TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		var userID, spaceID string
		var reserved int64
		err := tx.QueryRowContext(context.Background(), `UPDATE hosted_ai_reservations SET status='released',settled_at=NOW() WHERE id=$1 AND status='reserved' RETURNING user_id,COALESCE(space_id,''),reserved_microusd`, reservationID).Scan(&userID, &spaceID, &reserved)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(context.Background(), `UPDATE hosted_ai_wallets SET reserved_microusd=GREATEST(0,reserved_microusd-$2),updated_at=NOW() WHERE user_id=$1`, userID, reserved); err != nil {
			return err
		}
		if spaceID != "" {
			_, err = tx.ExecContext(context.Background(), `UPDATE space_hosted_ai_wallets SET reserved_microusd=GREATEST(0,reserved_microusd-$2),updated_at=NOW() WHERE space_id=$1`, spaceID, reserved)
		}
		return err
	})
}

func (db *Database) ReleaseCreditReservation(reservationID string) error {
	return db.ReleaseHostedAIReservation(reservationID)
}

func (db *Database) SettleHostedAIReservation(reservationID, idempotencyKey string, usage HostedAIUsage) (*HostedAIWallet, error) {
	charge := usage.ChargeMicrousd
	if charge <= 0 {
		charge = usage.Credits
	}
	if charge < 1 {
		charge = 1
	}
	var wallet HostedAIWallet
	err := db.TestingWithRLSContext(context.Background(), TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		var userID, spaceID, meter, status string
		var reserved int64
		if err := tx.QueryRowContext(context.Background(), `SELECT user_id,COALESCE(space_id,''),meter,reserved_microusd,status FROM hosted_ai_reservations WHERE id=$1 FOR UPDATE`, reservationID).Scan(&userID, &spaceID, &meter, &reserved, &status); err != nil {
			return err
		}
		if err := tx.QueryRowContext(context.Background(), `SELECT user_id,weekly_allowance_microusd,weekly_remaining_microusd,reserved_microusd,reset_at FROM hosted_ai_wallets WHERE user_id=$1 FOR UPDATE`, userID).
			Scan(&wallet.UserID, &wallet.WeeklyAllowanceMicrousd, &wallet.WeeklyRemainingMicrousd, &wallet.ReservedMicrousd, &wallet.ResetAt); err != nil {
			return err
		}
		var spaceWallet SpaceHostedAIWallet
		if spaceID != "" {
			if err := tx.QueryRowContext(context.Background(), `SELECT space_id,weekly_allowance_microusd,weekly_remaining_microusd,reserved_microusd,reset_at FROM space_hosted_ai_wallets WHERE space_id=$1 FOR UPDATE`, spaceID).
				Scan(&spaceWallet.SpaceID, &spaceWallet.WeeklyAllowanceMicrousd, &spaceWallet.WeeklyRemainingMicrousd, &spaceWallet.ReservedMicrousd, &spaceWallet.ResetAt); err != nil {
				return err
			}
		}
		if status != "reserved" {
			return nil
		}
		maximumCharge := wallet.WeeklyRemainingMicrousd - (wallet.ReservedMicrousd - reserved)
		if maximumCharge < 0 {
			maximumCharge = 0
		}
		if spaceID != "" {
			spaceMaximum := spaceWallet.WeeklyRemainingMicrousd - (spaceWallet.ReservedMicrousd - reserved)
			if spaceMaximum < maximumCharge {
				maximumCharge = max(int64(0), spaceMaximum)
			}
		}
		if charge > maximumCharge {
			charge = maximumCharge
		}
		if _, err := tx.ExecContext(context.Background(), `UPDATE hosted_ai_wallets SET weekly_remaining_microusd=weekly_remaining_microusd-$2,reserved_microusd=GREATEST(0,reserved_microusd-$3),updated_at=NOW() WHERE user_id=$1`, userID, charge, reserved); err != nil {
			return err
		}
		if spaceID != "" {
			if _, err := tx.ExecContext(context.Background(), `UPDATE space_hosted_ai_wallets SET weekly_remaining_microusd=weekly_remaining_microusd-$2,reserved_microusd=GREATEST(0,reserved_microusd-$3),updated_at=NOW() WHERE space_id=$1`, spaceID, charge, reserved); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(context.Background(), `UPDATE hosted_ai_reservations SET status='settled',settled_at=NOW() WHERE id=$1`, reservationID); err != nil {
			return err
		}
		rateCardVersion := strings.TrimSpace(envconfig.Getenv("MISTY_HOSTED_AI_RATE_CARD_VERSION"))
		if rateCardVersion == "" {
			rateCardVersion = HostedAIRateCardVersion
		}
		_, err := tx.ExecContext(context.Background(), `INSERT INTO hosted_ai_usage_ledger(id,user_id,space_id,reservation_id,source,meter,weekly_delta_microusd,provider,model,input_tokens,cached_input_tokens,output_tokens,reasoning_tokens,rate_card_version,provider_cost_microusd,charged_microusd,idempotency_key)
			VALUES($1,$2,NULLIF($3,''),$4,'consumption',$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16) ON CONFLICT(idempotency_key) DO NOTHING`, uuid.NewString(), userID, spaceID, reservationID, meter, -charge, usage.Provider, usage.Model, usage.InputTokens, usage.CachedInputTokens, usage.OutputTokens, usage.ReasoningTokens, rateCardVersion, usage.ProviderCost, charge, idempotencyKey)
		if err == nil {
			wallet.WeeklyRemainingMicrousd -= charge
			wallet.ReservedMicrousd -= reserved
		}
		return err
	})
	return &wallet, err
}

func (db *Database) SettleCreditReservation(reservationID, idempotencyKey string, usage CreditUsage) (*CreditWallet, error) {
	return db.SettleHostedAIReservation(reservationID, idempotencyKey, usage)
}

func (db *Database) HostedAIUsageByMeter(userID string, since time.Time) ([]HostedAIUsageSummary, error) {
	var summaries []HostedAIUsageSummary
	err := db.TestingWithRLSContext(context.Background(), userRLSSettings(userID), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(context.Background(), `SELECT meter,COALESCE(SUM(charged_microusd),0) FROM hosted_ai_usage_ledger WHERE user_id=$1 AND source='consumption' AND created_at>=$2 GROUP BY meter ORDER BY meter`, userID, since)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var summary HostedAIUsageSummary
			if err := rows.Scan(&summary.Meter, &summary.ChargedMicrousd); err != nil {
				return err
			}
			summaries = append(summaries, summary)
		}
		return rows.Err()
	})
	return summaries, err
}

func (db *Database) SpaceHostedAIUsageByMeter(spaceID string, since time.Time) ([]HostedAIUsageSummary, error) {
	var summaries []HostedAIUsageSummary
	err := db.TestingWithRLSContext(context.Background(), TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(context.Background(), `SELECT meter,COALESCE(SUM(charged_microusd),0) FROM hosted_ai_usage_ledger WHERE space_id=$1 AND source='consumption' AND created_at>=$2 GROUP BY meter ORDER BY meter`, spaceID, since)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var summary HostedAIUsageSummary
			if err := rows.Scan(&summary.Meter, &summary.ChargedMicrousd); err != nil {
				return err
			}
			summaries = append(summaries, summary)
		}
		return rows.Err()
	})
	return summaries, err
}

func (db *Database) CreditUsageByMeter(userID string, since time.Time) ([]CreditUsageSummary, error) {
	return db.HostedAIUsageByMeter(userID, since)
}

var ErrCreditPurchasesRetired = errors.New("credit purchases are retired")

func (db *Database) AddPurchasedCredits(string, string, string, int64) error {
	return ErrCreditPurchasesRetired
}

func (db *Database) RecordCreditPurchase(CreditPurchase) error { return ErrCreditPurchasesRetired }

func (db *Database) GetCreditPurchaseByPaymentIntent(string) (*CreditPurchase, error) {
	return nil, nil
}

func (db *Database) RefundCreditPurchase(*CreditPurchase) error { return nil }

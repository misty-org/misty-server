package db

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
)

type HostedAILimitReachedError struct {
	Required  int64
	Available int64
}

func (e HostedAILimitReachedError) Error() string { return "hosted AI weekly limit reached" }

// InsufficientCreditsError remains as a source-compatibility alias while older
// clients roll off. It is never serialized to customers.
type InsufficientCreditsError = HostedAILimitReachedError

const (
	// The "assistant_ai" value is deliberately unchanged by the agent rename: it
	// is written into hosted-AI usage and credit ledger rows, so renaming it
	// would orphan every historical usage record and break rate-card lookups for
	// past billing periods. Only the Go identifier was renamed.
	HostedAIMeterAgent         = "assistant_ai"
	HostedAIMeterAutomation    = "automation_ai"
	HostedAIMeterSemanticQuery = "semantic_query"
	HostedAIRateCardVersion    = "2026-07-22-weekly-microusd-v1"
	MicrousdPerDollar          = int64(1_000_000)

	CreditMeterAgentAI      = HostedAIMeterAgent
	CreditMeterAutomationAI = HostedAIMeterAutomation
	CreditRateCardVersion   = HostedAIRateCardVersion
	CreditDenominationScale = int64(1_000)
)

type HostedAIWallet struct {
	UserID                  string    `json:"-"`
	WeeklyAllowanceMicrousd int64     `json:"-"`
	WeeklyRemainingMicrousd int64     `json:"-"`
	ReservedMicrousd        int64     `json:"-"`
	ResetAt                 time.Time `json:"reset_at"`
}

func (wallet HostedAIWallet) Available() int64 {
	available := wallet.WeeklyRemainingMicrousd - wallet.ReservedMicrousd
	if available < 0 {
		return 0
	}
	return available
}

func (wallet HostedAIWallet) UsedRatio() float64 {
	if wallet.WeeklyAllowanceMicrousd <= 0 {
		return 1
	}
	used := wallet.WeeklyAllowanceMicrousd - wallet.WeeklyRemainingMicrousd
	if used <= 0 {
		return 0
	}
	if used >= wallet.WeeklyAllowanceMicrousd {
		return 1
	}
	return float64(used) / float64(wallet.WeeklyAllowanceMicrousd)
}

type CreditWallet = HostedAIWallet

type HostedAIUsage struct {
	Provider          string
	Model             string
	InputTokens       int64
	CachedInputTokens int64
	OutputTokens      int64
	ReasoningTokens   int64
	ProviderCost      int64
	ChargeMicrousd    int64
	// Credits is accepted only by compatibility callers and means micro-USD.
	Credits int64
}

type CreditUsage = HostedAIUsage

type HostedAIReservation struct {
	ID               string
	UserID           string
	ReservedMicrousd int64
	ReservedCredits  int64
	Status           string
}

type CreditReservation = HostedAIReservation

type HostedAIUsageSummary struct {
	Meter           string `json:"meter"`
	ChargedMicrousd int64  `json:"-"`
}

type CreditUsageSummary = HostedAIUsageSummary

// CreditPurchase is retained only so legacy webhook code can compile during
// the compatibility window. New purchases are permanently disabled.
type CreditPurchase struct {
	ID, UserID, StripeCheckoutSessionID, StripePaymentIntentID, PackID, Status string
	Credits                                                                    int64
}

func WeeklyHostedAIAllowance(tier Tier) int64 {
	return EntitlementsForTier(tier).WeeklyHostedAIAllowance
}

func MonthlyCreditAllowance(tier Tier) int64 { return WeeklyHostedAIAllowance(tier) }

func nextWeeklyReset(now time.Time) time.Time {
	now = now.UTC()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	days := (8 - int(midnight.Weekday())) % 7
	if days == 0 {
		days = 7
	}
	return midnight.AddDate(0, 0, days)
}

func (db *Database) GetOrCreateHostedAIWallet(userID string, tier Tier, now time.Time) (*HostedAIWallet, error) {
	allowance := WeeklyHostedAIAllowance(tier)
	now = now.UTC()
	var wallet HostedAIWallet
	err := db.TestingWithRLSContext(context.Background(), TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(context.Background(), `
			WITH released AS (
				UPDATE hosted_ai_reservations SET status='released',settled_at=NOW()
				WHERE user_id=$1 AND status='reserved' AND created_at<NOW()-INTERVAL '15 minutes'
				RETURNING reserved_microusd
			)
			UPDATE hosted_ai_wallets SET reserved_microusd=GREATEST(0,reserved_microusd-COALESCE((SELECT SUM(reserved_microusd) FROM released),0))
			WHERE user_id=$1`, userID); err != nil {
			return err
		}
		result, err := tx.ExecContext(context.Background(), `
			INSERT INTO hosted_ai_wallets(user_id,weekly_allowance_microusd,weekly_remaining_microusd,reset_at)
			VALUES($1,$2,$2,$3) ON CONFLICT(user_id) DO NOTHING`, userID, allowance, nextWeeklyReset(now))
		if err != nil {
			return err
		}
		inserted, _ := result.RowsAffected()
		var priorAllowance, priorRemaining int64
		var priorReset time.Time
		if err := tx.QueryRowContext(context.Background(), `SELECT weekly_allowance_microusd,weekly_remaining_microusd,reset_at FROM hosted_ai_wallets WHERE user_id=$1 FOR UPDATE`, userID).
			Scan(&priorAllowance, &priorRemaining, &priorReset); err != nil {
			return err
		}
		resetDue := inserted == 0 && !priorReset.After(now)
		remaining := priorRemaining
		resetAt := priorReset
		if resetDue {
			remaining = allowance
			resetAt = nextWeeklyReset(now)
		} else if priorAllowance != allowance {
			used := priorAllowance - priorRemaining
			if used < 0 {
				used = 0
			}
			remaining = allowance - used
			if remaining < 0 {
				remaining = 0
			}
		}
		if _, err := tx.ExecContext(context.Background(), `UPDATE hosted_ai_wallets SET weekly_allowance_microusd=$2,weekly_remaining_microusd=$3,reset_at=$4,updated_at=NOW() WHERE user_id=$1`, userID, allowance, remaining, resetAt); err != nil {
			return err
		}
		if err := tx.QueryRowContext(context.Background(), `SELECT user_id,weekly_allowance_microusd,weekly_remaining_microusd,reserved_microusd,reset_at FROM hosted_ai_wallets WHERE user_id=$1`, userID).
			Scan(&wallet.UserID, &wallet.WeeklyAllowanceMicrousd, &wallet.WeeklyRemainingMicrousd, &wallet.ReservedMicrousd, &wallet.ResetAt); err != nil {
			return err
		}
		if inserted > 0 || resetDue {
			return insertHostedAILedgerEntry(tx, userID, "weekly_grant", allowance, "weekly_grant:"+userID+":"+wallet.ResetAt.Format(time.RFC3339))
		}
		if remaining != priorRemaining {
			return insertHostedAILedgerEntry(tx, userID, "plan_adjustment", remaining-priorRemaining, "plan_adjustment:"+userID+":"+now.Format(time.RFC3339Nano))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &wallet, nil
}

func (db *Database) GetOrCreateCreditWallet(userID string, tier Tier, now time.Time) (*CreditWallet, error) {
	return db.GetOrCreateHostedAIWallet(userID, tier, now)
}

func (db *Database) StartSubscriptionCreditPeriod(userID string, tier Tier, activatedAt time.Time, _ string) (*CreditWallet, error) {
	return db.GetOrCreateHostedAIWallet(userID, tier, activatedAt)
}

func insertHostedAILedgerEntry(tx *sql.Tx, userID, source string, delta int64, idempotencyKey string) error {
	_, err := tx.ExecContext(context.Background(), `INSERT INTO hosted_ai_usage_ledger(id,user_id,source,weekly_delta_microusd,idempotency_key)
		VALUES($1,$2,$3,$4,$5) ON CONFLICT(idempotency_key) DO NOTHING`, uuid.NewString(), userID, source, delta, idempotencyKey)
	return err
}

package db

import (
	"context"
	"database/sql"
	"time"
)

const (
	BasicStorageBytes = int64(2_000_000_000)
	ProStorageBytes   = int64(50_000_000_000)
	MaxStorageBytes   = int64(250_000_000_000)

	BasicSpaceLimit = 3
	ProSpaceLimit   = 10
	MaxSpaceLimit   = 0 // Unlimited.

	BasicWeeklyAgentAllowance = int64(150_000)
	ProWeeklyAgentAllowance   = BasicWeeklyAgentAllowance * 6
	MaxWeeklyAgentAllowance   = ProWeeklyAgentAllowance * 2

	// Compatibility names for internal callers while the persisted hosted-AI
	// wallet schema retains its existing identifiers.
	FreeStorageBytes            = BasicStorageBytes
	FreeWeeklyHostedAIAllowance = BasicWeeklyAgentAllowance
	ProWeeklyHostedAIAllowance  = ProWeeklyAgentAllowance
	MaxWeeklyHostedAIAllowance  = MaxWeeklyAgentAllowance
)

type PlanEntitlements struct {
	Plan                      Tier  `json:"plan"`
	StorageLimitBytes         int64 `json:"storage_limit_bytes"`
	WeeklyHostedAIAllowance   int64 `json:"-"`
	SpaceLimit                int   `json:"space_limit"`
	UnlimitedSpaces           bool  `json:"unlimited_spaces"`
	UnlimitedCollaborators    bool  `json:"unlimited_collaborators"`
	UnlimitedAgentDefinitions bool  `json:"unlimited_agent_definitions"`
}

func NormalizePlan(tier Tier) Tier {
	switch tier {
	case TierPersonal, TierPro:
		return TierPro
	case TierMax:
		return TierMax
	default:
		return TierBasic
	}
}

func EntitlementsForTier(tier Tier) PlanEntitlements {
	plan := NormalizePlan(tier)
	entitlements := PlanEntitlements{
		Plan: plan, UnlimitedCollaborators: true,
		UnlimitedAgentDefinitions: true,
	}
	switch plan {
	case TierMax:
		entitlements.StorageLimitBytes = MaxStorageBytes
		entitlements.WeeklyHostedAIAllowance = MaxWeeklyAgentAllowance
		entitlements.SpaceLimit = MaxSpaceLimit
		entitlements.UnlimitedSpaces = true
	case TierPro:
		entitlements.StorageLimitBytes = ProStorageBytes
		entitlements.WeeklyHostedAIAllowance = ProWeeklyAgentAllowance
		entitlements.SpaceLimit = ProSpaceLimit
	default:
		entitlements.StorageLimitBytes = BasicStorageBytes
		entitlements.WeeklyHostedAIAllowance = BasicWeeklyAgentAllowance
		entitlements.SpaceLimit = BasicSpaceLimit
	}
	return entitlements
}

func entitlementsForUserTx(ctx context.Context, tx *sql.Tx, userID string, now time.Time) (PlanEntitlements, error) {
	var tier Tier
	var status string
	var expiresAt sql.NullTime
	if err := tx.QueryRowContext(ctx, `SELECT tier,status,expires_at FROM licenses WHERE user_id=$1`, userID).Scan(&tier, &status, &expiresAt); err != nil {
		return PlanEntitlements{}, err
	}
	if status == LicenseStatusTrialing && expiresAt.Valid && !expiresAt.Time.After(now.UTC()) {
		tier = TierBasic
	}
	return EntitlementsForTier(tier), nil
}

// addSpaceMembershipTx is the canonical gate for every operation that creates
// a Space membership. The per-user transaction lock makes creation and invite
// acceptance serialize against each other, so concurrent requests cannot
// exceed the member's own plan limit.
func addSpaceMembershipTx(ctx context.Context, tx *sql.Tx, spaceID, userID, role string) error {
	if role != "owner" && role != "member" {
		return ErrSpaceInvalid
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "space-memberships:"+userID); err != nil {
		return err
	}
	entitlements, err := entitlementsForUserTx(ctx, tx, userID, time.Now())
	if err != nil {
		return err
	}
	if !entitlements.UnlimitedSpaces {
		var memberships int
		// The permanent Misty Space is product infrastructure, not one of the
		// user's plan-limited collaborative Spaces.
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM space_members m
			JOIN spaces s ON s.id=m.space_id WHERE m.user_id=$1 AND s.kind='standard'`, userID).Scan(&memberships); err != nil {
			return err
		}
		if memberships >= entitlements.SpaceLimit {
			return ErrSpaceLimit
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO space_members(space_id,user_id,role) VALUES($1,$2,$3)`, spaceID, userID, role)
	return err
}

func (db *Database) EntitlementsForUser(ctx context.Context, userID string) (PlanEntitlements, error) {
	var entitlements PlanEntitlements
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		var err error
		entitlements, err = entitlementsForUserTx(ctx, tx, userID, time.Now())
		return err
	})
	return entitlements, err
}

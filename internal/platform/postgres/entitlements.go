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
	MaxSpaceLimit   = 10

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
	Plan                            Tier  `json:"plan"`
	MaxOwnedSpaces                  int   `json:"max_owned_spaces"`
	PersonalStorageLimitBytes       int64 `json:"personal_storage_limit_bytes"`
	SpaceStorageLimitBytes          int64 `json:"space_storage_limit_bytes"`
	PersonalWeeklyHostedAIAllowance int64 `json:"personal_ai_limit"`
	SpaceWeeklyHostedAIAllowance    int64 `json:"space_ai_limit"`

	// Compatibility fields retained for clients that have not yet adopted the
	// explicit personal-vs-Space entitlement names. SpaceLimit now means owned
	// Spaces; joining a Space is unlimited for every plan.
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
		entitlements.PersonalStorageLimitBytes = MaxStorageBytes
		entitlements.PersonalWeeklyHostedAIAllowance = MaxWeeklyAgentAllowance
		entitlements.MaxOwnedSpaces = MaxSpaceLimit
	case TierPro:
		entitlements.PersonalStorageLimitBytes = ProStorageBytes
		entitlements.PersonalWeeklyHostedAIAllowance = ProWeeklyAgentAllowance
		entitlements.MaxOwnedSpaces = ProSpaceLimit
	default:
		entitlements.PersonalStorageLimitBytes = BasicStorageBytes
		entitlements.PersonalWeeklyHostedAIAllowance = BasicWeeklyAgentAllowance
		entitlements.MaxOwnedSpaces = BasicSpaceLimit
	}
	// The concepts are independent even though the launch values are equal.
	entitlements.SpaceStorageLimitBytes = entitlements.PersonalStorageLimitBytes
	entitlements.SpaceWeeklyHostedAIAllowance = entitlements.PersonalWeeklyHostedAIAllowance
	entitlements.StorageLimitBytes = entitlements.PersonalStorageLimitBytes
	entitlements.WeeklyHostedAIAllowance = entitlements.PersonalWeeklyHostedAIAllowance
	entitlements.SpaceLimit = entitlements.MaxOwnedSpaces
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

// addSpaceMembershipTx is the canonical write path for Space memberships.
// Only ownership consumes a plan allowance; ordinary membership is unlimited.
func addSpaceMembershipTx(ctx context.Context, tx *sql.Tx, spaceID, userID, role string) error {
	if role != "owner" && role != "member" {
		return ErrSpaceInvalid
	}
	if role == "owner" {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "spaces:owner:"+userID); err != nil {
			return err
		}
		entitlements, err := entitlementsForUserTx(ctx, tx, userID, time.Now())
		if err != nil {
			return err
		}
		var memberships int
		// The permanent Misty Space is product infrastructure, not one of the
		// user's plan-limited owned collaborative Spaces. The Space being
		// created already exists in this transaction, hence the strict > check.
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM spaces
			WHERE owner_user_id=$1 AND kind='standard' AND lifecycle_state<>'deleted'`, userID).Scan(&memberships); err != nil {
			return err
		}
		if memberships > entitlements.MaxOwnedSpaces {
			return ErrSpaceOwnershipLimit
		}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO space_members(space_id,user_id,role) VALUES($1,$2,$3)`, spaceID, userID, role)
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

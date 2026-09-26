package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/kannachi323/misty/server/internal/billingadapter"
	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
	"time"
)

type PlanEntitlements struct {
	BillingAvailable                bool  `json:"-"`
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

// Resource ceilings are customer-visible adapter results. The public server
// supplies no commercial defaults. Disabled self-hosting has no plan restriction.
func unrestrictedEntitlements() PlanEntitlements {
	return PlanEntitlements{Plan: TierBasic, MaxOwnedSpaces: 2147483647, SpaceLimit: 2147483647, PersonalStorageLimitBytes: 9007199254740991, SpaceStorageLimitBytes: 9007199254740991, StorageLimitBytes: 9007199254740991, UnlimitedSpaces: true, UnlimitedCollaborators: true, UnlimitedAgentDefinitions: true, BillingAvailable: true}
}
func entitlementsForUserTx(ctx context.Context, _ *sql.Tx, userID string, _ time.Time) (PlanEntitlements, error) {
	adapter, err := envconfig.BillingAdapter()
	if err != nil {
		return PlanEntitlements{}, err
	}
	limits := unrestrictedEntitlements()
	if !adapter.Enabled() {
		return limits, nil
	}
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	decision, err := adapter.Do(readCtx, "summary", billingadapter.Request{Version: 1, AccountID: userID, Operation: "customer.summary", OperationID: "summary", Key: "summary"})
	if err != nil {
		limits.BillingAvailable = false
		return limits, nil
	}
	var response struct {
		Entitlements *PlanEntitlements `json:"entitlements"`
	}
	if err = json.Unmarshal(decision.Summary, &response); err != nil || response.Entitlements == nil {
		limits.BillingAvailable = false
		return limits, nil
	}
	limits = *response.Entitlements
	if limits.MaxOwnedSpaces < 0 || limits.PersonalStorageLimitBytes < 0 || limits.SpaceStorageLimitBytes < 0 {
		return PlanEntitlements{}, billingadapter.ErrUnavailable
	}
	limits.BillingAvailable = true
	return limits, nil
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
		if !entitlements.BillingAvailable {
			return billingadapter.ErrUnavailable
		}
		var memberships int
		// The Space being created already exists in this transaction, hence the
		// strict > check.
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM spaces
			WHERE owner_user_id=$1 AND lifecycle_state<>'deleted'`, userID).Scan(&memberships); err != nil {
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

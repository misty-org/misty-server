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
	SpaceStorageLimitBytes          int64 `json:"space_storage_limit_bytes,omitempty"`
	PersonalWeeklyHostedAIAllowance int64 `json:"personal_ai_limit"`
	// WeeklyExternalCodingAllowance is the most of the weekly hosted
	// allowance (in universal tokens) that coding through the person's own
	// tools may use. Basic plans have none.
	WeeklyExternalCodingAllowance int64 `json:"external_coding_allowance"`
	// Coding is whether the plan includes coding at all: projects, coding
	// runs and pushes. Pro and above; always on without a billing service.
	Coding bool `json:"coding"`

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

// DevelopmentExternalCodingAllowance is the coding allowance without a billing
// service. Production reads each plan's allowance from the billing service.
const DevelopmentExternalCodingAllowance int64 = 20_000_000

// Resource ceilings are customer-visible billing results. Production always
// asks the billing service; development and tests without one receive these
// finite defaults.
func defaultEntitlements() PlanEntitlements {
	return PlanEntitlements{Plan: TierBasic, MaxOwnedSpaces: 2147483647, SpaceLimit: 2147483647, UnlimitedSpaces: true, UnlimitedCollaborators: true, UnlimitedAgentDefinitions: true, BillingAvailable: true}
}
func entitlementsForUserTx(ctx context.Context, _ *sql.Tx, userID string, _ time.Time) (PlanEntitlements, error) {
	adapter, err := envconfig.BillingAdapter()
	if err != nil {
		return PlanEntitlements{}, err
	}
	limits := defaultEntitlements()
	if !adapter.Enabled() {
		// Without a billing service (development and tests) coding through
		// the person's own tools has a finite allowance of its own.
		limits.WeeklyExternalCodingAllowance = DevelopmentExternalCodingAllowance
		limits.Coding = true
		return limits, nil
	}
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	decision, err := adapter.Do(readCtx, "summary", billingadapter.Request{Version: 1, AccountID: userID, Operation: "customer.summary", OperationID: "summary", Key: "summary"})
	if err != nil {
		return PlanEntitlements{}, billingadapter.ErrUnavailable
	}
	var response struct {
		Entitlements *PlanEntitlements `json:"entitlements"`
	}
	if err = json.Unmarshal(decision.Summary, &response); err != nil || response.Entitlements == nil {
		return PlanEntitlements{}, billingadapter.ErrUnavailable
	}
	limits = *response.Entitlements
	if limits.MaxOwnedSpaces < 0 || limits.PersonalStorageLimitBytes < 0 || limits.SpaceStorageLimitBytes < 0 || limits.PersonalStorageLimitBytes >= 9007199254740991 || limits.SpaceStorageLimitBytes >= 9007199254740991 {
		return PlanEntitlements{}, billingadapter.ErrUnavailable
	}
	// Coding is a Pro-and-above feature, whatever the billing answer says.
	limits.Coding = limits.Plan.IncludesCoding()
	if !limits.Coding || limits.WeeklyExternalCodingAllowance < 0 {
		limits.WeeklyExternalCodingAllowance = 0
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

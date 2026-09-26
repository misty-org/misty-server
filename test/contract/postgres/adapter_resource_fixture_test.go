package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kannachi323/misty/server/internal/billingadapter"
	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// These are arbitrary operator-supplied ceilings used to exercise concurrent
// resource accounting. No commercial allowance calculation runs in the server.
const FreeStorageBytes = int64(2_000_000_000)
const BasicSpaceLimit = 3
const MaxSpaceLimit = 10
const ProSpaceLimit = 10
const BasicStorageBytes = int64(2_000_000_000)
const ProStorageBytes = int64(50_000_000_000)
const MaxStorageBytes = int64(250_000_000_000)

func useResourceAdapterFixture(t *testing.T, database *Database) {
	t.Helper()
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req billingadapter.Request
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			http.Error(w, "invalid", 400)
			return
		}
		// Existing tests assign fixture cohorts using their historical tier field.
		var tier string
		if err := database.Conn.QueryRow("SELECT tier FROM licenses WHERE user_id=$1", req.AccountID).Scan(&tier); err != nil {
			http.Error(w, "unknown account", 404)
			return
		}
		limit, spaces := BasicStorageBytes, 3
		if tier == "pro" || tier == "personal" {
			limit, spaces = ProStorageBytes, 10
		}
		if tier == "max" {
			limit, spaces = MaxStorageBytes, 10
		}
		raw, _ := json.Marshal(map[string]any{"entitlements": PlanEntitlements{Plan: Tier(tier), MaxOwnedSpaces: spaces, SpaceLimit: spaces, PersonalStorageLimitBytes: limit, SpaceStorageLimitBytes: limit, StorageLimitBytes: limit, UnlimitedCollaborators: true, UnlimitedAgentDefinitions: true}})
		json.NewEncoder(w).Encode(billingadapter.Decision{Allowed: true, Summary: raw})
	}))
	t.Cleanup(endpoint.Close)
	t.Setenv("MISTY_BILLING_ADAPTER", "http")
	t.Setenv("MISTY_BILLING_URL", endpoint.URL)
	t.Setenv("MISTY_BILLING_SECRET", "resource-fixture-01234567890123456789")
	t.Setenv("MISTY_ENVIRONMENT", "development")
}

// Historical labels choose fake adapter responses for upgrade fixtures only.
func setAdapterFixtureCohort(database *Database, licenseID string, tier Tier, status string, expiresAt *time.Time) error {
	return database.TestingWithRLSContext(context.Background(), TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE licenses SET tier=$2,status=$3,expires_at=$4 WHERE id=$1`, licenseID, tier, status, expiresAt)
		return err
	})
}

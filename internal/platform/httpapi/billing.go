package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/billingadapter"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// Billing handlers translate authenticated account requests into the optional
// adapter contract. They never query wallets, Stripe or commercial licenses.
func customerBillingRequest(r *http.Request, userID, action string) billingadapter.Request {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		key = uuid.NewString()
	}
	return billingadapter.Request{Version: 1, AccountID: userID, Operation: "customer." + action, OperationID: key, Key: key}
}
func customerBillingAction(database *db.Database, action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		userID, err := sessionUserID(r, database)
		if err != nil {
			http.Error(w, "internal error", 500)
			return
		}
		if userID == "" {
			http.Error(w, "not authenticated", 401)
			return
		}
		service := database.BillingService()
		if !service.Adapter.Enabled() {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "billing_disabled"})
			return
		}
		request := customerBillingRequest(r, userID, action)
		if action == "checkout" {
			var body struct {
				Tier     string `json:"tier"`
				Interval string `json:"interval"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			request.Selection = &billingadapter.Selection{Product: body.Tier, Interval: body.Interval}
			user, e := database.GetUserByID(userID)
			if e != nil || user == nil {
				http.Error(w, "account unavailable", 503)
				return
			}
			request.Customer = &billingadapter.Customer{Email: user.Email, Name: user.Name}
		}
		d, err := service.Adapter.Do(r.Context(), action, request)
		if err != nil {
			writeBillingError(w, err)
			return
		}
		if len(d.Summary) == 0 {
			writeBillingError(w, billingadapter.ErrUnavailable)
			return
		}
		writeJSON(w, http.StatusOK, d.Summary)
	}
}
func CreateCheckoutSession(database *db.Database) http.HandlerFunc {
	return customerBillingAction(database, "checkout")
}
func CreatePortalSession(database *db.Database) http.HandlerFunc {
	return customerBillingAction(database, "portal")
}

// Reads of account identity never depend on the billing service being healthy.
// Only paid operations fail closed; ordinary browser/session access continues.
func accountBillingSummary(ctx context.Context, database *db.Database, userID string) (map[string]any, error) {
	service := database.BillingService()
	if !service.Adapter.Enabled() {
		return map[string]any{"tier": "basic", "status": "active", "trial_eligible": false, "billing": map[string]any{"kind": "disabled", "customer_portal_available": false}, "ai": map[string]any{"available": true, "paused": false, "used_ratio": 0}}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	d, err := service.Adapter.Do(ctx, "summary", billingadapter.Request{Version: 1, AccountID: userID, Operation: "customer.summary", OperationID: "summary", Key: "summary"})
	if err != nil {
		return nil, err
	}
	var summary map[string]any
	if err = json.Unmarshal(d.Summary, &summary); err != nil {
		return nil, err
	}
	return summary, nil
}
func GetBillingUsage(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		userID, err := sessionUserID(r, database)
		if err != nil {
			http.Error(w, "internal error", 500)
			return
		}
		if userID == "" {
			http.Error(w, "not authenticated", 401)
			return
		}
		summary, err := accountBillingSummary(r.Context(), database, userID)
		if err != nil {
			writeBillingError(w, err)
			return
		}
		storage, err := database.OwnerStorageUsage(r.Context(), userID)
		if err != nil {
			http.Error(w, "storage usage unavailable", 503)
			return
		}
		spaces, err := database.ListSpaces(r.Context(), userID)
		if err != nil {
			http.Error(w, "Space usage unavailable", 503)
			return
		}
		spaceUsage := []map[string]any{}
		for _, space := range spaces {
			usage, e := database.SpaceStorageUsage(r.Context(), userID, space.ID)
			if e != nil {
				http.Error(w, "Space usage unavailable", 503)
				return
			}
			spaceUsage = append(spaceUsage, map[string]any{"space_id": space.ID, "name": space.Name, "role": space.Role, "owner_user_id": space.OwnerUserID, "storage": usage})
		}
		ai, _ := summary["ai"].(map[string]any)
		ratio, _ := ai["used_ratio"].(float64)
		writeJSON(w, http.StatusOK, map[string]any{"enabled": database.BillingService().Adapter.Enabled(), "plan": summary["tier"], "storage": storage, "spaces": spaceUsage, "entitlements": summary["entitlements"], "personal": map[string]any{"ai": ai, "storage": storage.Personal}, "billing": summary["billing"], "agent_usage": map[string]any{"percentage_used": ratio * 100, "available": ai["available"], "paused": ai["paused"], "reset_at": ai["reset_at"], "plan": summary["tier"]}})
	}
}

package api

import (
	"errors"
	"net/http"
	"time"

	appbilling "github.com/kannachi323/misty/server/internal/billing"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func CreateCheckoutSession(database *db.Database) http.HandlerFunc {
	service := appbilling.NewService(database)

	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := sessionUserID(r, database)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if userID == "" {
			http.Error(w, "not authenticated", http.StatusUnauthorized)
			return
		}

		var body struct {
			Tier     db.Tier                    `json:"tier"`
			Interval appbilling.BillingInterval `json:"interval"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}

		url, err := service.CreateCheckoutSession(userID, body.Tier, body.Interval)
		switch {
		case errors.Is(err, appbilling.ErrInvalidTier):
			http.Error(w, "invalid tier", http.StatusBadRequest)
			return
		case errors.Is(err, appbilling.ErrInvalidInterval):
			http.Error(w, "invalid billing interval", http.StatusBadRequest)
			return
		case errors.Is(err, appbilling.ErrSubscriptionExists):
			http.Error(w, "active subscription already exists", http.StatusConflict)
			return
		case errors.Is(err, appbilling.ErrCheckoutInProgress):
			http.Error(w, "subscription checkout already in progress", http.StatusConflict)
			return
		case errors.Is(err, appbilling.ErrUserNotFound):
			http.Error(w, "user not found", http.StatusNotFound)
			return
		case err != nil:
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"url": url})
	}
}

func CreateCreditCheckoutSession(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := sessionUserID(r, database)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if userID == "" {
			http.Error(w, "not authenticated", http.StatusUnauthorized)
			return
		}
		writeJSON(w, http.StatusGone, map[string]any{"code": "retired_product", "message": "AI agent usage add-ons are no longer sold."})
	}
}

func CreatePortalSession(database *db.Database) http.HandlerFunc {
	service := appbilling.NewService(database)
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := sessionUserID(r, database)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if userID == "" {
			http.Error(w, "not authenticated", http.StatusUnauthorized)
			return
		}
		url, err := service.CreatePortalSession(userID)
		if errors.Is(err, appbilling.ErrPortalUnavailable) {
			http.Error(w, "customer portal unavailable", http.StatusConflict)
			return
		}
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"url": url})
	}
}

func GetBillingUsage(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		userID, err := sessionUserID(r, database)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if userID == "" {
			http.Error(w, "not authenticated", http.StatusUnauthorized)
			return
		}
		license, err := database.GetLicenseByUserID(userID)
		if err != nil || license == nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		wallet, err := database.GetOrCreateHostedAIWallet(userID, license.Tier, time.Now())
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		storage, err := database.OwnerStorageUsage(r.Context(), userID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		plan := db.NormalizePlan(license.Tier)
		available := wallet.Available() > 0
		payload := map[string]any{
			"plan": plan, "storage": storage,
			"agent_usage": map[string]any{
				"percentage_used": wallet.UsedRatio() * 100,
				"available":       available,
				"paused":          !available,
				"reset_at":        wallet.ResetAt,
				"plan":            plan,
			},
			// Retained for existing clients during the response-field migration.
			"hosted_ai": map[string]any{"used_ratio": wallet.UsedRatio(), "reset_at": wallet.ResetAt},
		}
		if subscription, subscriptionErr := database.GetStripeSubscriptionByUserID(userID); subscriptionErr != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		} else if subscription != nil {
			payload["subscription"] = map[string]any{"status": subscription.Status, "current_period_end": subscription.CurrentPeriodEnd,
				"cancel_at_period_end": subscription.CancelAtPeriodEnd, "billing_interval": subscription.BillingInterval}
		}
		if license.Status == db.LicenseStatusTrialing {
			payload["trial"] = map[string]any{"status": license.Status, "ends_at": license.ExpiresAt}
		}
		writeJSON(w, http.StatusOK, payload)
	}
}

func StartPersonalTrial(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := sessionUserID(r, database)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if userID == "" {
			http.Error(w, "not authenticated", http.StatusUnauthorized)
			return
		}

		writeJSON(w, http.StatusGone, map[string]any{"code": "trial_checkout_required", "message": "Start the 14-day Pro trial through checkout."})
	}
}

package api

import (
	"errors"
	"net/http"
	"time"

	appbilling "github.com/kannachi323/misty/server/internal/billing"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type billingAIUsage struct {
	Used      int64     `json:"used"`
	Reserved  int64     `json:"reserved"`
	Limit     int64     `json:"limit"`
	Remaining int64     `json:"remaining"`
	UsedRatio float64   `json:"used_ratio"`
	Available bool      `json:"available"`
	Paused    bool      `json:"paused"`
	ResetAt   time.Time `json:"reset_at"`
}

type billingSpaceUsage struct {
	SpaceID     string                `json:"space_id"`
	Name        string                `json:"name"`
	Role        string                `json:"role"`
	OwnerUserID string                `json:"owner_user_id"`
	Storage     *db.SpaceStorageUsage `json:"storage"`
	AI          billingAIUsage        `json:"ai"`
}

func personalBillingAIUsage(wallet *db.HostedAIWallet) billingAIUsage {
	used := wallet.WeeklyAllowanceMicrousd - wallet.WeeklyRemainingMicrousd
	if used < 0 {
		used = 0
	}
	remaining := wallet.Available()
	return billingAIUsage{
		Used: used, Reserved: wallet.ReservedMicrousd,
		Limit: wallet.WeeklyAllowanceMicrousd, Remaining: remaining,
		UsedRatio: wallet.UsedRatio(), Available: remaining > 0, Paused: remaining == 0,
		ResetAt: wallet.ResetAt,
	}
}

func spaceBillingAIUsage(wallet *db.SpaceHostedAIWallet) billingAIUsage {
	used := wallet.WeeklyAllowanceMicrousd - wallet.WeeklyRemainingMicrousd
	if used < 0 {
		used = 0
	}
	remaining := wallet.Available()
	return billingAIUsage{
		Used: used, Reserved: wallet.ReservedMicrousd,
		Limit: wallet.WeeklyAllowanceMicrousd, Remaining: remaining,
		UsedRatio: wallet.UsedRatio(), Available: remaining > 0, Paused: remaining == 0,
		ResetAt: wallet.ResetAt,
	}
}

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
		entitlements, err := database.EntitlementsForUser(r.Context(), userID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		spaces, err := database.ListSpaces(r.Context(), userID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		spaceUsage := make([]billingSpaceUsage, 0, len(spaces))
		for _, space := range spaces {
			spaceStorage, storageErr := database.SpaceStorageUsage(r.Context(), userID, space.ID)
			if storageErr != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			spaceWallet, walletErr := database.GetOrCreateSpaceHostedAIWallet(space.ID, time.Now())
			if walletErr != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			spaceUsage = append(spaceUsage, billingSpaceUsage{
				SpaceID: space.ID, Name: space.Name, Role: space.Role, OwnerUserID: space.OwnerUserID,
				Storage: spaceStorage, AI: spaceBillingAIUsage(spaceWallet),
			})
		}
		plan := db.NormalizePlan(license.Tier)
		available := wallet.Available() > 0
		personalAI := personalBillingAIUsage(wallet)
		payload := map[string]any{
			"plan": plan, "storage": storage, "entitlements": entitlements,
			"personal": map[string]any{"storage": storage.Personal, "ai": personalAI},
			"spaces":   spaceUsage,
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

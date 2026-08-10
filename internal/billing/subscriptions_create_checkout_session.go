package billing

import (
	"context"
	"errors"
	"strings"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/stripe/stripe-go/v82"
	checkoutsession "github.com/stripe/stripe-go/v82/checkout/session"
)

func (service *Service) CreateCheckoutSession(userID string, tier db.Tier, interval BillingInterval) (string, error) {
	if !TestingValidPaidTier(tier) {
		return "", ErrInvalidTier
	}
	if !TestingValidInterval(interval) {
		return "", ErrInvalidInterval
	}
	user, err := service.database.GetUserByID(userID)
	if err != nil {
		return "", err
	}
	if user == nil {
		return "", ErrUserNotFound
	}
	existing, err := service.database.GetStripeSubscriptionByUserID(userID)
	if err != nil {
		return "", err
	}
	if existing != nil && db.SubscriptionAllowsPaidAccess(existing.Status) {
		return "", ErrSubscriptionExists
	}
	if existing == nil {
		completedCheckout, err := service.database.HasCompletedSubscriptionCheckoutWithoutSubscription(
			context.Background(),
			userID,
		)
		if err != nil {
			return "", err
		}
		if completedCheckout {
			return "", ErrSubscriptionExists
		}
	}
	cfg, err := TestingLoadStripeCheckoutConfig()
	if err != nil {
		return "", err
	}
	customerID, err := service.database.GetStripeCustomerIDForUser(userID)
	if err != nil {
		return "", err
	}
	license, err := service.database.GetLicenseByUserID(userID)
	if err != nil {
		return "", err
	}
	hasPurchase, err := service.database.HasCompletedStripePurchase(userID)
	if err != nil {
		return "", err
	}
	// The one-time trial remains a Pro benefit. Max Checkout never receives an
	// automatic trial, even when the account would otherwise be eligible.
	trialEligible := tier == db.TierPro && license != nil && license.TrialStartedAt == nil && !hasPurchase
	attempt, _, err := service.database.BeginSubscriptionCheckout(
		context.Background(),
		user.ID,
		user.LicenseID,
		tier,
		string(interval),
		time.Now(),
		35*time.Minute,
	)
	if err != nil {
		return "", err
	}
	if attempt.Tier != tier || attempt.BillingInterval != string(interval) {
		return "", ErrCheckoutInProgress
	}
	if attempt.Status == "open" && attempt.CheckoutURL != "" &&
		attempt.ExpiresAt.After(time.Now()) {
		return attempt.CheckoutURL, nil
	}
	if attempt.Status == "open" {
		// Resolve an overdue attempt from Stripe before releasing it. This is
		// read-only and covers a delayed or missed completion/expiration event.
		canonical, fetchErr := service.fetchCheckout(
			cfg,
			attempt.StripeCheckoutSessionID,
		)
		if fetchErr != nil || canonical == nil {
			return "", ErrCheckoutInProgress
		}
		switch canonical.Status {
		case stripe.CheckoutSessionStatusComplete:
			if err := service.database.CompleteSubscriptionCheckoutBySessionID(
				context.Background(),
				attempt.StripeCheckoutSessionID,
			); err != nil {
				return "", err
			}
			return "", ErrSubscriptionExists
		case stripe.CheckoutSessionStatusExpired:
			if err := service.database.ExpireSubscriptionCheckoutBySessionID(
				context.Background(),
				attempt.StripeCheckoutSessionID,
			); err != nil {
				return "", err
			}
			return service.CreateCheckoutSession(userID, tier, interval)
		default:
			return "", ErrCheckoutInProgress
		}
	}
	result, err := service.createCheckout(
		cfg,
		user,
		tier,
		interval,
		customerID,
		trialEligible,
		attempt.ID,
		attempt.ExpiresAt,
	)
	if err != nil {
		// Keep the attempt recoverable. A retry uses the same Stripe
		// idempotency key, so an ambiguous network failure cannot create a
		// second Checkout Session.
		return "", err
	}
	if result.ID == "" || result.URL == "" {
		return "", errors.New("Stripe returned an incomplete Checkout Session")
	}
	if result.ExpiresAt.IsZero() {
		result.ExpiresAt = attempt.ExpiresAt
	}
	if err := service.database.OpenSubscriptionCheckout(
		context.Background(),
		attempt.ID,
		result.ID,
		result.URL,
		result.ExpiresAt,
	); err != nil {
		return "", err
	}
	return result.URL, nil
}

func (service *Service) CreateCreditCheckoutSession(userID, packID string) (string, error) {
	return "", ErrInvalidCreditPack
}

func (service *Service) CreatePortalSession(userID string) (string, error) {
	cfg, err := TestingLoadStripeCheckoutConfig()
	if err != nil {
		return "", err
	}
	customerID, err := service.database.GetStripeCustomerIDForUser(userID)
	if err != nil {
		return "", err
	}
	if customerID == "" {
		return "", ErrPortalUnavailable
	}
	return service.createPortal(cfg.secretKey, customerID, cfg.portalReturnURL)
}

func (service *Service) StartProTrial(userID string) (*db.License, error) {
	license, err := service.database.GetLicenseByUserID(userID)
	if err != nil {
		return nil, err
	}
	if license == nil {
		return nil, ErrLicenseNotFound
	}
	if license.TrialStartedAt != nil || license.Tier != db.TierBasic || license.Status != db.LicenseStatusActive {
		return nil, ErrTrialUnavailable
	}
	hasPurchase, err := service.database.HasCompletedStripePurchase(userID)
	if err != nil {
		return nil, err
	}
	if hasPurchase {
		return nil, ErrTrialUnavailable
	}
	started, err := service.database.StartTrialByUserID(userID, service.trialDuration)
	if err != nil {
		return nil, err
	}
	if !started {
		return nil, ErrTrialUnavailable
	}
	return service.database.GetLicenseByUserID(userID)
}

// StartPersonalTrial remains as a compatibility shim for existing callers.
func (service *Service) StartPersonalTrial(userID string) (*db.License, error) {
	return service.StartProTrial(userID)
}

func createStripeCheckoutSession(
	cfg CheckoutConfig,
	user *db.User,
	tier db.Tier,
	interval BillingInterval,
	customerID string,
	trialEligible bool,
	idempotencyKey string,
	expiresAt time.Time,
) (CheckoutSessionResult, error) {
	stripe.Key = cfg.secretKey
	params := TestingStripeCheckoutSessionParams(cfg, user, tier, interval, customerID, trialEligible)
	params.ExpiresAt = stripe.Int64(expiresAt.UTC().Unix())
	params.SetIdempotencyKey("misty-subscription-checkout-" + idempotencyKey)
	session, err := checkoutsession.New(params)
	if err != nil {
		return CheckoutSessionResult{}, err
	}
	return CheckoutSessionResult{
		ID: session.ID, URL: session.URL, ExpiresAt: time.Unix(session.ExpiresAt, 0).UTC(),
	}, nil
}

func fetchStripeCheckoutSession(
	cfg CheckoutConfig,
	sessionID string,
) (*stripe.CheckoutSession, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, errors.New("Stripe Checkout Session id is required")
	}
	stripe.Key = cfg.secretKey
	return checkoutsession.Get(sessionID, nil)
}

func TestingStripeCheckoutSessionParams(cfg CheckoutConfig, user *db.User, tier db.Tier, interval BillingInterval, customerID string, trialEligible bool) *stripe.CheckoutSessionParams {
	metadata := map[string]string{"user_id": user.ID, "license_id": user.LicenseID, "tier": string(tier), "interval": string(interval), "kind": "subscription"}
	params := &stripe.CheckoutSessionParams{Mode: stripe.String(string(stripe.CheckoutSessionModeSubscription)),
		SuccessURL: stripe.String(cfg.successURL), CancelURL: stripe.String(cfg.cancelURL), ClientReferenceID: stripe.String(user.ID),
		Metadata: metadata, SubscriptionData: &stripe.CheckoutSessionSubscriptionDataParams{Metadata: metadata},
		LineItems: []*stripe.CheckoutSessionLineItemParams{{Price: stripe.String(cfg.TestingPrices[TestingPriceKey{TestingTier: tier, TestingInterval: interval}]), Quantity: stripe.Int64(1)}}}
	if tier == db.TierPro && trialEligible {
		params.SubscriptionData.TrialPeriodDays = stripe.Int64(int64(ProTrialDuration / (24 * time.Hour)))
		params.PaymentMethodCollection = stripe.String(string(stripe.CheckoutSessionPaymentMethodCollectionAlways))
	}
	if customerID != "" {
		params.Customer = stripe.String(customerID)
	} else {
		params.CustomerEmail = stripe.String(user.Email)
	}
	return params
}

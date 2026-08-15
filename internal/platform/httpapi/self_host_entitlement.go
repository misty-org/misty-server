package api

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
	"github.com/kannachi323/misty/server/internal/platform/entitlement"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

const entitlementHeader = "X-Misty-Self-Hosted-Entitlement"

func MintSelfHostedEntitlement(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if InstanceConfigFromEnv().Deployment != "hosted" {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
			return
		}
		userID, ok := authenticatedUser(w, r, database)
		if !ok {
			return
		}
		expiresAt, eligible, err := selfHostEligibility(database, userID, time.Now().UTC())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "internal_error"})
			return
		}
		if !eligible {
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "self_host_entitlement_ineligible"})
			return
		}
		signer, subjectSecret, err := entitlementSignerFromEnv()
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "self_host_entitlement_unavailable"})
			return
		}
		now := time.Now().UTC()
		maximum := now.Add(entitlement.MaxLifetime)
		if expiresAt.IsZero() || expiresAt.After(maximum) {
			expiresAt = maximum
		}
		mac := hmac.New(sha256.New, subjectSecret)
		_, _ = mac.Write([]byte(userID))
		token, err := signer.Sign(entitlement.Claims{
			Subject:       "license_" + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)),
			Status:        "eligible",
			IssuedAt:      now.Unix(),
			ExpiresAt:     expiresAt.Unix(),
			TokenID:       "entitlement_" + uuid.NewString(),
			SchemaVersion: entitlement.SchemaVersion,
			Issuer:        entitlement.Issuer,
			Audience:      entitlement.Audience,
		})
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "self_host_entitlement_unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"token": token, "expires_at": expiresAt})
	}
}

func selfHostEligibility(database *db.Database, userID string, now time.Time) (time.Time, bool, error) {
	license, err := database.GetLicenseByUserID(userID)
	if err != nil {
		return time.Time{}, false, err
	}
	if license != nil && license.Status == db.LicenseStatusTrialing && license.ExpiresAt != nil && license.ExpiresAt.After(now) {
		return license.ExpiresAt.UTC(), true, nil
	}
	subscription, err := database.GetStripeSubscriptionByUserID(userID)
	if err != nil {
		return time.Time{}, false, err
	}
	if subscription == nil || !db.SubscriptionAllowsPaidAccess(subscription.Status) {
		return time.Time{}, false, nil
	}
	if subscription.CurrentPeriodEnd != nil {
		if !subscription.CurrentPeriodEnd.After(now) {
			return time.Time{}, false, nil
		}
		return subscription.CurrentPeriodEnd.UTC(), true, nil
	}
	return now.Add(entitlement.MaxLifetime), true, nil
}

func entitlementSignerFromEnv() (entitlement.Signer, []byte, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(envconfig.Getenv("MISTY_SELF_HOST_ENTITLEMENT_PRIVATE_KEY")))
	if err != nil {
		return entitlement.Signer{}, nil, err
	}
	parsed, err := x509.ParsePKCS8PrivateKey(raw)
	if err != nil {
		return entitlement.Signer{}, nil, err
	}
	privateKey, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return entitlement.Signer{}, nil, entitlement.ErrInvalid
	}
	keyID := strings.TrimSpace(envconfig.Getenv("MISTY_SELF_HOST_ENTITLEMENT_KEY_ID"))
	secret, err := base64.StdEncoding.DecodeString(strings.TrimSpace(envconfig.Getenv("MISTY_SELF_HOST_ENTITLEMENT_SUBJECT_SECRET")))
	if err != nil || len(secret) < 32 || keyID == "" {
		return entitlement.Signer{}, nil, entitlement.ErrInvalid
	}
	return entitlement.Signer{PrivateKey: privateKey, KeyID: keyID}, secret, nil
}

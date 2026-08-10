package api

import (
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"

	appbilling "github.com/kannachi323/misty/server/internal/billing"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/stripe/stripe-go/v82/webhook"
)

func StripeWebhook(database *db.Database) http.HandlerFunc {
	return StripeWebhookWithService(envconfig.Getenv("STRIPE_WEBHOOK_SECRET"), appbilling.NewStripeService(database))
}

// minimumStripeWebhookSecretLength rejects placeholder values. Stripe issues
// secrets of the form "whsec_..." that are far longer than this.
const minimumStripeWebhookSecretLength = 16

func StripeWebhookWithService(webhookSecret string, service *appbilling.StripeService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// An empty secret is still a valid HMAC key, so signature verification
		// would happily accept an event any attacker can sign — handing out
		// paid tiers and credits for free. Refuse to process anything until the
		// secret is actually configured.
		if len(strings.TrimSpace(webhookSecret)) < minimumStripeWebhookSecretLength {
			log.Println("Stripe webhook rejected: STRIPE_WEBHOOK_SECRET is not configured")
			http.Error(w, "webhook not configured", http.StatusServiceUnavailable)
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, 65536))
		if err != nil {
			http.Error(w, "failed to read body", http.StatusBadRequest)
			return
		}

		event, err := webhook.ConstructEventWithOptions(
			body,
			r.Header.Get("Stripe-Signature"),
			webhookSecret,
			webhook.ConstructEventOptions{
				IgnoreAPIVersionMismatch: true,
			},
		)
		if err != nil {
			log.Println("Stripe signature verification failed:", err)
			http.Error(w, "invalid signature", http.StatusBadRequest)
			return
		}

		createdAt := time.Time{}
		if event.Created > 0 {
			createdAt = time.Unix(event.Created, 0).UTC()
		}
		if err := service.HandleWebhookEventAt(
			event.ID,
			string(event.Type),
			createdAt,
			event.Data.Raw,
		); err != nil {
			log.Println("Stripe event processing failed:", err)
			http.Error(w, "event processing failed", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
	}
}

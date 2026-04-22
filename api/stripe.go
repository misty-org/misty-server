package api

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/kannachi323/misty/server/db"
	"github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/webhook"
)

func StripeWebhook(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 65536))
		if err != nil {
			http.Error(w, "failed to read body", http.StatusBadRequest)
			return
		}

		event, err := webhook.ConstructEvent(body, r.Header.Get("Stripe-Signature"), os.Getenv("STRIPE_WEBHOOK_SECRET"))
		if err != nil {
			log.Println("Stripe signature verification failed:", err)
			http.Error(w, "invalid signature", http.StatusBadRequest)
			return
		}

		switch event.Type {

		case "checkout.session.completed":
			var session stripe.CheckoutSession
			if err := json.Unmarshal(event.Data.Raw, &session); err != nil {
				log.Println("Failed to parse checkout session:", err)
				break
			}
			handleCheckoutCompleted(database, &session)

		case "customer.subscription.updated":
			var sub stripe.Subscription
			if err := json.Unmarshal(event.Data.Raw, &sub); err != nil {
				log.Println("Failed to parse subscription:", err)
				break
			}
			handleSubscriptionUpdated(database, &sub)

		case "customer.subscription.deleted":
			var sub stripe.Subscription
			if err := json.Unmarshal(event.Data.Raw, &sub); err != nil {
				log.Println("Failed to parse subscription:", err)
				break
			}
			handleSubscriptionDeleted(database, &sub)
		}

	w.WriteHeader(http.StatusOK)
	}
}

func tierFromCheckoutSession(session *stripe.CheckoutSession) db.Tier {
	if strings.EqualFold(session.Metadata["tier"], string(db.TierMax)) {
		return db.TierMax
	}
	return db.TierPro
}

func handleCheckoutCompleted(database *db.Database, session *stripe.CheckoutSession) {
	email := session.CustomerDetails.Email
	if email == "" {
		log.Println("Stripe checkout completed but no email on session")
		return
	}

	user, _, err := database.GetUserByEmail(email)
	if err != nil || user == nil {
		log.Printf("Stripe checkout: no Misty account found for %s", email)
		return
	}

	var expiresAt *time.Time
	if session.Subscription != nil && session.Subscription.CancelAt != 0 {
		t := time.Unix(session.Subscription.CancelAt, 0)
		expiresAt = &t
	}

	tier := tierFromCheckoutSession(session)
	if err := database.UpsertSubscription(user.ID, tier, "active", expiresAt); err != nil {
		log.Printf("Failed to provision %s for user %s: %v", tier, user.ID, err)
		return
	}

	log.Printf("Provisioned %s for user %s (%s)", tier, user.ID, email)
}

func handleSubscriptionUpdated(database *db.Database, sub *stripe.Subscription) {
	email := ""
	if sub.Customer != nil {
		email = sub.Customer.Email
	}
	if email == "" {
		return
	}

	user, _, err := database.GetUserByEmail(email)
	if err != nil || user == nil {
		return
	}

	status := string(sub.Status)
	var expiresAt *time.Time
	if sub.CancelAt != 0 {
		t := time.Unix(sub.CancelAt, 0)
		expiresAt = &t
	}

	tier := db.TierPro
	if sub.Status != stripe.SubscriptionStatusActive {
		tier = db.TierFree
	}

	if err := database.UpsertSubscription(user.ID, tier, status, expiresAt); err != nil {
		log.Printf("Failed to update subscription for user %s: %v", user.ID, err)
	}
}

func handleSubscriptionDeleted(database *db.Database, sub *stripe.Subscription) {
	email := ""
	if sub.Customer != nil {
		email = sub.Customer.Email
	}
	if email == "" {
		return
	}

	user, _, err := database.GetUserByEmail(email)
	if err != nil || user == nil {
		return
	}

	if err := database.UpsertSubscription(user.ID, db.TierFree, "cancelled", nil); err != nil {
		log.Printf("Failed to downgrade user %s: %v", user.ID, err)
	}

	log.Printf("Downgraded user %s (%s) to free", user.ID, email)
}

package api

import (
	"errors"
	"log"
	"net/http"
	"net/mail"
	"strings"

	"github.com/kannachi323/misty/server/db"
	"github.com/kannachi323/misty/server/email"
)

type WaitlistService struct {
	database           *db.Database
	emailSender        email.WaitlistSender
	notificationTarget string
}

func NewWaitlistService(database *db.Database, sender email.WaitlistSender, notificationTarget string) (*WaitlistService, error) {
	if database == nil {
		return nil, errors.New("waitlist database is required")
	}
	if sender == nil {
		return nil, errors.New("waitlist email sender is required")
	}

	return &WaitlistService{
		database:           database,
		emailSender:        sender,
		notificationTarget: strings.TrimSpace(notificationTarget),
	}, nil
}

func (s *WaitlistService) Join() http.HandlerFunc {
	type request struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		var body request
		if err := decodeJSON(w, r, &body); err != nil {
			http.Error(w, "email is required", http.StatusBadRequest)
			return
		}

		name := strings.TrimSpace(body.Name)
		emailAddress, err := normalizeWaitlistEmail(body.Email)
		if err != nil {
			http.Error(w, "valid email is required", http.StatusBadRequest)
			return
		}

		created, err := s.database.CreateWaitlistSignup(name, emailAddress)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		if err := s.emailSender.SendWaitlistConfirmationEmail(r.Context(), name, emailAddress); err != nil {
			log.Printf("waitlist confirmation email failed for %s: %v", emailAddress, err)
			http.Error(w, "could not send waitlist email", http.StatusBadGateway)
			return
		}

		if created && s.notificationTarget != "" {
			if err := s.emailSender.SendWaitlistNotificationEmail(r.Context(), s.notificationTarget, name, emailAddress); err != nil {
				log.Printf("waitlist notification email failed for %s: %v", emailAddress, err)
			}
		}

		writeJSON(w, http.StatusAccepted, map[string]string{
			"status":  "ok",
			"message": "You're on the waitlist. Check your email for confirmation.",
		})
	}
}

func normalizeWaitlistEmail(raw string) (string, error) {
	address, err := mail.ParseAddress(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}

	return strings.ToLower(strings.TrimSpace(address.Address)), nil
}

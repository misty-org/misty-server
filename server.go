package main

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	"github.com/kannachi323/misty/server/api"
	"github.com/kannachi323/misty/server/db"
	"github.com/kannachi323/misty/server/email"
)

type Server struct {
	Router                    *chi.Mux
	Database                  *db.Database
	EmailSender               email.Sender
	PasswordResetStartURL     string
	PasswordResetRedirectURL  string
	WaitlistNotificationEmail string
}

func CreateServer() (*Server, error) {
	passwordResetRedirectURL, err := passwordResetRedirectURLFromEnv()
	if err != nil {
		return nil, err
	}
	passwordResetStartURL, err := passwordResetStartURLFromEnv()
	if err != nil {
		return nil, err
	}

	s := &Server{
		Router:                    chi.NewRouter(),
		Database:                  &db.Database{},
		PasswordResetStartURL:     passwordResetStartURL,
		PasswordResetRedirectURL:  passwordResetRedirectURL,
		WaitlistNotificationEmail: strings.TrimSpace(os.Getenv("WAITLIST_NOTIFY_EMAIL")),
	}

	emailSender, err := email.NewSenderFromEnv()
	if err != nil {
		return nil, err
	}
	s.EmailSender = emailSender

	return s, nil
}

func (s *Server) MountHandlers() error {
	s.Router.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"https://*", "http://*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		AllowCredentials: true,
		MaxAge:           300,
	}))
	s.Router.Use(api.NewAPIRateLimiter().Middleware)

	passwordResetService, err := api.NewPasswordResetService(s.Database, s.EmailSender, s.PasswordResetStartURL, s.PasswordResetRedirectURL)
	if err != nil {
		return err
	}
	waitlistService, err := api.NewWaitlistService(s.Database, s.EmailSender, s.WaitlistNotificationEmail)
	if err != nil {
		return err
	}

	registerHandler := api.Register(s.Database)
	loginHandler := api.Login(s.Database)
	logoutHandler := api.Logout(s.Database)
	forgotPasswordHandler := passwordResetService.Forgot()
	startResetHandler := passwordResetService.Start()
	validateResetTokenHandler := passwordResetService.Validate()
	resetPasswordHandler := passwordResetService.Reset()
	waitlistJoinHandler := waitlistService.Join()

	// Account management
	s.Router.Post("/register", registerHandler)
	s.Router.Post("/login", loginHandler)
	s.Router.Post("/logout", logoutHandler)
	s.Router.Post("/auth/forgot", forgotPasswordHandler)
	s.Router.Get("/auth/reset/start", startResetHandler)
	s.Router.Get("/auth/reset/validate", validateResetTokenHandler)
	s.Router.Post("/auth/reset", resetPasswordHandler)
	s.Router.Post("/waitlist", waitlistJoinHandler)

	// Dashboard — authenticated endpoints
	s.Router.Get("/me", api.GetMe(s.Database))
	s.Router.Put("/me/profile", api.UpdateProfile(s.Database))
	s.Router.Put("/me/device", api.UpdateDevice(s.Database))

	// Compatibility routes for clients configured with the /api prefix.
	s.Router.Post("/api/register", registerHandler)
	s.Router.Post("/api/login", loginHandler)
	s.Router.Post("/api/logout", logoutHandler)
	s.Router.Post("/api/auth/forgot", forgotPasswordHandler)
	s.Router.Get("/api/auth/reset/start", startResetHandler)
	s.Router.Get("/api/auth/reset/validate", validateResetTokenHandler)
	s.Router.Post("/api/auth/reset", resetPasswordHandler)
	s.Router.Post("/api/waitlist", waitlistJoinHandler)
	s.Router.Get("/api/me", api.GetMe(s.Database))
	s.Router.Put("/api/me/profile", api.UpdateProfile(s.Database))
	s.Router.Put("/api/me/device", api.UpdateDevice(s.Database))

	// License validation — called by the local proxy
	s.Router.Post("/license/validate", api.ValidateLicense(s.Database))
	s.Router.Get("/subscription", api.GetSubscription(s.Database))

	// Stripe webhook — called by Stripe on payment events
	s.Router.Post("/stripe/webhook", api.StripeWebhook(s.Database))

	return nil
}

func passwordResetRedirectURLFromEnv() (string, error) {
	rawURL := os.Getenv("PASSWORD_RESET_URL")
	if rawURL == "" {
		rawURL = "http://localhost:5173/#/reset"
	}

	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid PASSWORD_RESET_URL: %w", err)
	}
	if parsedURL.Host == "" {
		return "", fmt.Errorf("PASSWORD_RESET_URL must include a host")
	}
	if parsedURL.Scheme == "https" {
		return parsedURL.String(), nil
	}
	if parsedURL.Scheme == "http" && isLocalhostHostname(parsedURL.Hostname()) {
		return parsedURL.String(), nil
	}

	return "", fmt.Errorf("PASSWORD_RESET_URL must use https unless it targets localhost")
}

func passwordResetStartURLFromEnv() (string, error) {
	rawURL := os.Getenv("PASSWORD_RESET_START_URL")
	if rawURL == "" {
		rawURL = "http://localhost:8080/auth/reset/start"
	}

	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid PASSWORD_RESET_START_URL: %w", err)
	}
	if parsedURL.Host == "" {
		return "", fmt.Errorf("PASSWORD_RESET_START_URL must include a host")
	}
	if parsedURL.Scheme == "https" {
		return parsedURL.String(), nil
	}
	if parsedURL.Scheme == "http" && isLocalhostHostname(parsedURL.Hostname()) {
		return parsedURL.String(), nil
	}

	return "", fmt.Errorf("PASSWORD_RESET_START_URL must use https unless it targets localhost")
}

func isLocalhostHostname(host string) bool {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

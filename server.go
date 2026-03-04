package main

import (
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	"github.com/kannachi323/misty/server/api"
	"github.com/kannachi323/misty/server/db"
)

type Server struct {
	Router   *chi.Mux
	Database *db.Database
}

func CreateServer() (*Server, error) {
	s := &Server{
		Router:   chi.NewRouter(),
		Database: &db.Database{},
	}
	return s, nil
}

func (s *Server) MountHandlers() {
	s.Router.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"https://*", "http://*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	// Account management
	s.Router.Post("/register", api.Register(s.Database))
	s.Router.Post("/login", api.Login(s.Database))

	// License validation — called by the local proxy
	s.Router.Post("/license/validate", api.ValidateLicense(s.Database))

	// Stripe webhook — called by Stripe on payment events
	s.Router.Post("/stripe/webhook", api.StripeWebhook(s.Database))
}

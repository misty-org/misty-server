package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kannachi323/misty/server/internal/platform/email"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

const genericForgotPasswordMessage = "If the account exists, a password reset email will be sent shortly."
const TestingPasswordResetCookieName = "misty_reset_token"

type PasswordResetService struct {
	database      *db.Database
	emailSender   email.PasswordResetSender
	startURL      string
	redirectURL   string
	forgotLimiter *ForgotPasswordRateLimiter
	now           func() time.Time
}

func NewPasswordResetService(database *db.Database, sender email.PasswordResetSender, startURL, redirectURL string) (*PasswordResetService, error) {
	if database == nil {
		return nil, errors.New("password reset database is required")
	}
	if sender == nil {
		return nil, errors.New("password reset email sender is required")
	}
	if err := TestingValidateResetURL(startURL); err != nil {
		return nil, err
	}
	if err := TestingValidateResetURL(redirectURL); err != nil {
		return nil, err
	}

	return &PasswordResetService{
		database:      database,
		emailSender:   sender,
		startURL:      startURL,
		redirectURL:   redirectURL,
		forgotLimiter: NewForgotPasswordRateLimiter(5, security.PasswordResetTokenTTL),
		now:           time.Now,
	}, nil
}

func (s *PasswordResetService) Forgot() http.HandlerFunc {
	type request struct {
		Email string `json:"email"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		var body request
		if decodeJSON(w, r, &body) != nil {
			return
		}

		emailAddress := strings.TrimSpace(body.Email)
		if emailAddress == "" {
			http.Error(w, "email is required", http.StatusBadRequest)
			return
		}

		if allowed, _ := s.forgotLimiter.Allow(TestingForgotPasswordRateLimitKey(r, emailAddress), s.now()); !allowed {
			http.Error(w, "too many reset requests", http.StatusTooManyRequests)
			return
		}

		s.handleForgotPassword(r.Context(), emailAddress)
		writeJSON(w, http.StatusAccepted, map[string]string{
			"status":  "ok",
			"message": genericForgotPasswordMessage,
		})
	}
}

func (s *PasswordResetService) Reset() http.HandlerFunc {
	type request struct {
		NewPassword string `json:"new_password"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		var body request
		if decodeJSON(w, r, &body) != nil {
			return
		}

		token, err := s.TestingReadResetTokenCookie(r)
		if err != nil {
			http.Error(w, "invalid or expired reset token", http.StatusBadRequest)
			return
		}
		if body.NewPassword == "" {
			http.Error(w, "new_password is required", http.StatusBadRequest)
			return
		}

		err = s.database.ResetPasswordWithToken(security.HashToken(token), body.NewPassword, s.now())
		switch {
		case errors.Is(err, db.ErrPasswordResetTokenInvalid):
			TestingClearPasswordResetCookie(w)
			http.Error(w, "invalid or expired reset token", http.StatusBadRequest)
			return
		case err != nil:
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		TestingClearPasswordResetCookie(w)
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func (s *PasswordResetService) Validate() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, err := s.TestingReadResetTokenCookie(r)
		if err != nil {
			http.Error(w, "invalid or expired reset token", http.StatusNotFound)
			return
		}

		err = s.database.ValidatePasswordResetToken(security.HashToken(token), s.now())
		switch {
		case errors.Is(err, db.ErrPasswordResetTokenInvalid):
			TestingClearPasswordResetCookie(w)
			http.Error(w, "invalid or expired reset token", http.StatusNotFound)
			return
		case err != nil:
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func (s *PasswordResetService) Start() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimSpace(r.URL.Query().Get("token"))
		if token == "" {
			http.Redirect(w, r, s.redirectURL, http.StatusSeeOther)
			return
		}

		err := s.database.ValidatePasswordResetToken(security.HashToken(token), s.now())
		switch {
		case errors.Is(err, db.ErrPasswordResetTokenInvalid):
			TestingClearPasswordResetCookie(w)
			http.Redirect(w, r, s.redirectURL, http.StatusSeeOther)
			return
		case err != nil:
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		http.SetCookie(w, TestingBuildPasswordResetCookie(token, s.now().Add(security.PasswordResetTokenTTL), TestingIsSecureRequest(r)))
		http.Redirect(w, r, s.redirectURL, http.StatusSeeOther)
	}
}

func (s *PasswordResetService) handleForgotPassword(ctx context.Context, emailAddress string) {
	user, _, err := s.database.GetUserByEmail(emailAddress)
	if err != nil {
		log.Printf("forgot password lookup failed for %q: %v", emailAddress, err)
		return
	}
	if user == nil {
		return
	}

	rawToken, err := security.GenerateSecureToken()
	if err != nil {
		log.Printf("forgot password token generation failed for user %s: %v", user.ID, err)
		return
	}

	if err := s.database.UpsertPasswordResetToken(user.ID, security.HashToken(rawToken), s.now().Add(security.PasswordResetTokenTTL)); err != nil {
		log.Printf("forgot password token storage failed for user %s: %v", user.ID, err)
		return
	}

	if err := s.sendPasswordResetEmail(ctx, user.Email, rawToken); err != nil {
		log.Printf("forgot password email send failed for user %s: %v", user.ID, err)
	}
}

func (s *PasswordResetService) sendPasswordResetEmail(ctx context.Context, emailAddress, token string) error {
	resetLink, err := s.buildResetLink(token)
	if err != nil {
		return err
	}

	return s.emailSender.SendPasswordResetEmail(ctx, emailAddress, resetLink)
}

func (s *PasswordResetService) buildResetLink(token string) (string, error) {
	resetURL, err := url.Parse(s.startURL)
	if err != nil {
		return "", err
	}

	query := resetURL.Query()
	query.Set("token", token)
	resetURL.RawQuery = query.Encode()
	return resetURL.String(), nil
}

func (s *PasswordResetService) TestingReadResetTokenCookie(r *http.Request) (string, error) {
	cookie, err := r.Cookie(TestingPasswordResetCookieName)
	if err != nil {
		return "", err
	}

	token := strings.TrimSpace(cookie.Value)
	if token == "" {
		return "", errors.New("password reset token cookie is empty")
	}

	return token, nil
}

func TestingBuildPasswordResetCookie(token string, expiresAt time.Time, secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     TestingPasswordResetCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
		Expires:  expiresAt,
		MaxAge:   int(time.Until(expiresAt).Seconds()),
	}
}

func TestingClearPasswordResetCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     TestingPasswordResetCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	})
}

func TestingValidateResetURL(rawURL string) error {
	resetURL, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	if resetURL.Host == "" {
		return errors.New("password reset URL must include a host")
	}
	if resetURL.Scheme == "https" {
		return nil
	}
	if resetURL.Scheme == "http" && isLocalhostHost(resetURL.Hostname()) {
		return nil
	}
	return errors.New("password reset URL must use https unless it targets localhost")
}

func isLocalhostHost(host string) bool {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

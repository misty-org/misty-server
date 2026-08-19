package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"

	mailintegration "github.com/kannachi323/misty/server/internal/integrations/mail"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

var (
	errMailProviderUnsupported = errors.New("mail provider is unsupported")
	errMailConfirmationNeeded  = errors.New("mail send confirmation is required")
)

type MailProviderFactory func(db.ConnectedAccount, string) (mailintegration.Provider, error)

func defaultMailProviderFactory(account db.ConnectedAccount, accessToken string) (mailintegration.Provider, error) {
	switch account.Provider {
	case "google":
		return mailintegration.NewGmail(mailintegration.GmailConfig{
			AccessToken: accessToken,
			AccountID:   account.AccountID,
		})
	case "microsoft":
		return mailintegration.NewOutlook(mailintegration.OutlookConfig{
			AccessToken: accessToken,
			AccountID:   account.AccountID,
		})
	default:
		return nil, errMailProviderUnsupported
	}
}

func (s *SpacesService) TestingSetMailProviderFactory(factory MailProviderFactory) {
	s.mailProviderFactory = factory
}

func (s *SpacesService) mailProvider(ctx context.Context, userID, connectionID string) (mailintegration.Provider, *db.ConnectedAccount, error) {
	connectionID = strings.TrimSpace(connectionID)
	if connectionID == "" {
		return nil, nil, db.ErrSpaceInvalid
	}
	account, err := s.database.ConnectedAccount(ctx, userID, connectionID)
	if err != nil {
		return nil, nil, err
	}
	if account.Provider != "google" && account.Provider != "microsoft" {
		return nil, account, errMailProviderUnsupported
	}
	accessToken, _, err := s.connectedAccountAccessTokenForCapability(ctx, userID, connectionID, "mail")
	if err != nil {
		return nil, account, err
	}
	factory := s.mailProviderFactory
	if factory == nil {
		factory = defaultMailProviderFactory
	}
	provider, err := factory(*account, accessToken)
	return provider, account, err
}

func mailErrorCode(err error) string {
	switch {
	case errors.Is(err, db.ErrSpaceNotFound):
		return "mail_connection_not_found"
	case errors.Is(err, db.ErrSpaceForbidden):
		return "mail_capability_required"
	case errors.Is(err, db.ErrSpaceInvalid), errors.Is(err, mailintegration.ErrInvalidInput), errors.Is(err, mailintegration.ErrInvalidConfiguration):
		return "mail_invalid_request"
	case errors.Is(err, errMailProviderUnsupported):
		return "mail_provider_unsupported"
	case errors.Is(err, errMailConfirmationNeeded):
		return "mail_confirmation_required"
	case errors.Is(err, mailintegration.ErrBodyTooLarge):
		return "mail_body_too_large"
	case errors.Is(err, mailintegration.ErrResponseTooLarge):
		return "mail_response_too_large"
	}
	var providerError *mailintegration.ProviderError
	if errors.As(err, &providerError) {
		providerCode := strings.ToLower(strings.TrimSpace(providerError.Code))
		providerMessage := strings.ToLower(strings.TrimSpace(providerError.Message))
		if providerCode == "mailboxnotenabledforrestapi" ||
			providerCode == "errormailboxnotenabledforrestapi" ||
			(strings.Contains(providerMessage, "mailbox") &&
				(strings.Contains(providerMessage, "not enabled") ||
					strings.Contains(providerMessage, "not licensed") ||
					strings.Contains(providerMessage, "not found"))) {
			return "mail_provider_mailbox_unavailable"
		}
		switch providerError.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return "mail_provider_authorization_failed"
		case http.StatusNotFound:
			return "mail_provider_item_not_found"
		case http.StatusTooManyRequests:
			return "mail_provider_rate_limited"
		default:
			return "mail_provider_unavailable"
		}
	}
	return "mail_provider_unavailable"
}

func writeMailError(w http.ResponseWriter, err error) {
	code := mailErrorCode(err)
	// Cloudflare replaces origin 502 responses with its own body. That drops
	// Misty's CORS headers and turns a useful provider error into a misleading
	// browser-level CORS failure. A mail provider is a failed dependency, so 424
	// is also the more accurate status and preserves our JSON error contract.
	status := http.StatusFailedDependency
	var providerError *mailintegration.ProviderError
	if errors.As(err, &providerError) {
		log.Printf("mail provider failure: code=%s provider_code=%s provider_status=%d", code,
			strings.TrimSpace(providerError.Code), providerError.StatusCode)
	} else {
		log.Printf("mail provider failure: code=%s", code)
	}
	switch code {
	case "mail_connection_not_found", "mail_provider_item_not_found":
		status = http.StatusNotFound
	case "mail_capability_required":
		status = http.StatusForbidden
	case "mail_invalid_request":
		status = http.StatusBadRequest
	case "mail_provider_unsupported":
		status = http.StatusUnprocessableEntity
	case "mail_provider_mailbox_unavailable":
		status = http.StatusUnprocessableEntity
	case "mail_confirmation_required":
		status = http.StatusConflict
	case "mail_body_too_large", "mail_response_too_large":
		status = http.StatusRequestEntityTooLarge
	case "mail_provider_rate_limited":
		status = http.StatusTooManyRequests
	case "mail_provider_authorization_failed":
		status = http.StatusFailedDependency
	}
	writeJSON(w, status, map[string]string{"code": code})
}

package api

import (
	"context"
	"errors"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"net/http"
)

func writeProviderFailure(w http.ResponseWriter, err error) {
	if errors.Is(err, db.ErrSpaceForbidden) {
		writeSpaceError(w, err)
		return
	}
	var googleErr *googleAPIError
	var providerErr *providerAPIError
	if errors.As(err, &googleErr) || errors.As(err, &providerErr) {
		status := http.StatusBadGateway
		providerStatus := 0
		if googleErr != nil {
			providerStatus = googleErr.Status
		} else if providerErr != nil {
			providerStatus = providerErr.Status
		}
		if providerStatus == http.StatusUnauthorized || providerStatus == http.StatusForbidden {
			status = http.StatusFailedDependency
		} else if providerStatus == http.StatusPreconditionFailed {
			status = http.StatusConflict
		}
		writeJSON(w, status, map[string]string{"code": providerErrorCode(err)})
		return
	}
	writeJSON(w, http.StatusBadGateway, map[string]string{"code": "provider_error", "message": err.Error()})
}

func providerErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var googleErr *googleAPIError
	var providerErr *providerAPIError
	status := 0
	if errors.As(err, &googleErr) {
		status = googleErr.Status
	} else if errors.As(err, &providerErr) {
		status = providerErr.Status
	}
	switch status {
	case http.StatusUnauthorized:
		return "connection_revoked"
	case http.StatusForbidden:
		return "permission_missing"
	case http.StatusTooManyRequests:
		return "rate_limited"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusGone:
		return "cursor_expired"
	case http.StatusPreconditionFailed:
		return "conflict"
	}
	if errors.Is(err, db.ErrSpaceConflict) {
		return "conflict"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "provider_timeout"
	}
	return "provider_error"
}

package api

import (
	"errors"
	"net/http"

	"github.com/kannachi323/misty/server/internal/billingadapter"
)

func writeBillingError(w http.ResponseWriter, err error) {
	status, code := http.StatusServiceUnavailable, "billing_unavailable"
	if errors.Is(err, billingadapter.ErrDenied) {
		status, code = http.StatusPaymentRequired, "billing_admission_denied"
	}
	if errors.Is(err, billingadapter.ErrInvalid) {
		status, code = http.StatusBadRequest, "invalid_billing_request"
	}
	if errors.Is(err, billingadapter.ErrConflict) {
		status, code = http.StatusConflict, "billing_request_conflict"
	}
	writeJSON(w, status, map[string]string{"code": code})
}

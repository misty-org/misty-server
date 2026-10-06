package api

import (
	"errors"
	"net/http"

	"github.com/kannachi323/misty/server/internal/billingadapter"
)

func writeBillingError(w http.ResponseWriter, err error) {
	status, code := http.StatusServiceUnavailable, "billing_unavailable"
	message := "Usage checks are temporarily unavailable. Try again shortly."
	if errors.Is(err, billingadapter.ErrDenied) {
		status, code = http.StatusPaymentRequired, "billing_admission_denied"
		message = "Your account or command budget cannot cover this request. Check Account usage or try a smaller request."
	}
	if errors.Is(err, billingadapter.ErrInvalid) {
		status, code = http.StatusBadRequest, "invalid_billing_request"
		message = "This request could not be measured. Try again."
	}
	if errors.Is(err, billingadapter.ErrConflict) {
		status, code = http.StatusConflict, "billing_request_conflict"
		message = "This request conflicts with an existing usage reservation. Refresh its status before retrying."
	}
	writeJSON(w, status, map[string]string{"code": code, "message": message})
}

package api

import (
	"errors"
	"net/http"

	"github.com/kannachi323/misty/server/internal/billingadapter"
)

// usageLimitMessages explain a refusal in plain language. Clients format the
// reset time themselves from resets_at.
var usageLimitMessages = map[string]string{
	"window":  "You've used this session's AI. It refills a few hours after the session started.",
	"weekly":  "You've used this week's AI. It refills at the start of next week.",
	"monthly": "You've reached this month's AI limit. Room frees up as older usage ages out.",
	"command": "This task is bigger than one task may use. Try splitting it into smaller requests.",
	"service": "Misty's AI is busy right now. Try again in a little while.",
	"model":   "That model isn't available. Choose another in Settings.",
	// Too many new agent tasks at once, not an allowance: it clears as tasks finish.
	"concurrency": "Too many agent tasks are running at once. Wait for one to finish, then try again.",
	"rate":        "You're starting agent tasks faster than they can run. Wait a minute, then try again.",
	"email":       "Confirm your email to start using AI. Check your inbox for the link.",
}

func writeBillingError(w http.ResponseWriter, err error) {
	status, code := http.StatusServiceUnavailable, "billing_unavailable"
	message := "Usage checks are temporarily unavailable. Try again shortly."
	response := map[string]any{}
	var denial *billingadapter.Denial
	if errors.Is(err, billingadapter.ErrDenied) {
		status, code = http.StatusPaymentRequired, "billing_admission_denied"
		message = "Your account or command budget cannot cover this request. Check Account usage or try a smaller request."
		if errors.As(err, &denial) && denial.Limit != "" {
			code = "usage_limit_reached"
			if denial.Limit == "email" {
				code = "email_unverified"
			}
			if text, ok := usageLimitMessages[denial.Limit]; ok {
				message = text
			}
			response["limit"] = denial.Limit
			if denial.ResetsAt != nil {
				response["resets_at"] = denial.ResetsAt
			}
		}
	}
	if errors.Is(err, billingadapter.ErrInvalid) {
		status, code = http.StatusBadRequest, "invalid_billing_request"
		message = "This request could not be measured. Try again."
	}
	if errors.Is(err, billingadapter.ErrConflict) {
		status, code = http.StatusConflict, "billing_request_conflict"
		message = "This request conflicts with an existing usage reservation. Refresh its status before retrying."
	}
	response["code"], response["message"] = code, message
	writeJSON(w, status, response)
}

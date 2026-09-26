// Package billingadapter defines the optional, provider-neutral commercial boundary.
// It contains no prices, allowances, currency conversion, or subscription policy.
package billingadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrDenied      = errors.New("billing admission denied")
	ErrUnavailable = errors.New("billing service unavailable")
	ErrConflict    = errors.New("billing request conflict")
	ErrInvalid     = errors.New("invalid billing operation")
)

type Usage struct {
	Provider  string           `json:"provider,omitempty"`
	Model     string           `json:"model,omitempty"`
	Units     map[string]int64 `json:"units,omitempty"`
	Estimated bool             `json:"estimated,omitempty"`
}

// AccountID must come from server authentication, never from the request body.
// OperationID identifies the work; Key identifies one idempotent state transition.
type Request struct {
	Customer       *Customer  `json:"customer,omitempty"`
	Selection      *Selection `json:"selection,omitempty"`
	Version        int        `json:"version"`
	AccountID      string     `json:"account_id"`
	Operation      string     `json:"operation"`
	OperationID    string     `json:"operation_id"`
	Key            string     `json:"key"`
	ReservationID  string     `json:"reservation_id,omitempty"`
	ReservationIDs []string   `json:"reservation_ids,omitempty"`
	Usage          Usage      `json:"usage"`
	Reason         string     `json:"reason,omitempty"`
}
type Customer struct {
	Email string `json:"email"`
	Name  string `json:"name"`
}
type Selection struct {
	Product  string `json:"product"`
	Interval string `json:"interval"`
}
type Decision struct {
	Allowed       bool            `json:"allowed"`
	ReservationID string          `json:"reservation_id,omitempty"`
	Summary       json.RawMessage `json:"summary,omitempty"`
}
type Adapter interface {
	Enabled() bool
	Do(context.Context, string, Request) (Decision, error)
}
type Disabled struct{}

func (Disabled) Enabled() bool { return false }
func (Disabled) Do(_ context.Context, action string, request Request) (Decision, error) {
	if err := validate(action, request); err != nil {
		return Decision{}, err
	}
	return Decision{Allowed: true}, nil
}
func validate(action string, r Request) error {
	switch action {
	case "check", "reserve", "settle", "settle_group", "release_group", "release", "refund", "summary", "checkout", "portal", "close", "provision":
	default:
		return ErrInvalid
	}
	if r.Customer != nil && (len(r.Customer.Email) > 320 || len(r.Customer.Name) > 256) {
		return ErrInvalid
	}
	if r.Selection != nil && (len(r.Selection.Product) > 128 || len(r.Selection.Interval) > 32) {
		return ErrInvalid
	}
	if r.Version != 1 || strings.TrimSpace(r.AccountID) == "" || strings.TrimSpace(r.Operation) == "" || strings.TrimSpace(r.OperationID) == "" || strings.TrimSpace(r.Key) == "" {
		return ErrInvalid
	}
	if len(r.AccountID) > 256 || len(r.Key) > 512 || len(r.OperationID) > 512 || len(r.Reason) > 1024 {
		return ErrInvalid
	}
	for _, n := range r.Usage.Units {
		if n < 0 {
			return fmt.Errorf("%w: negative usage", ErrInvalid)
		}
	}
	if (action == "settle" || action == "release" || action == "refund") && r.ReservationID == "" {
		return ErrInvalid
	}
	if action == "settle_group" || action == "release_group" {
		if len(r.ReservationIDs) == 0 || len(r.ReservationIDs) > 128 {
			return ErrInvalid
		}
		seen := map[string]bool{}
		for _, id := range r.ReservationIDs {
			if id == "" || len(id) > 512 || seen[id] {
				return ErrInvalid
			}
			seen[id] = true
		}
	}
	return nil
}

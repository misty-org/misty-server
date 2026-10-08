package billingadapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

// Reservation carries authenticated identity with the opaque provider ID. It is
// persisted before a provider call, so settlement never needs fresh admission.
type Reservation struct {
	ID        string  `json:"id"`
	Admission Request `json:"admission"`
	// OverLimit means the work was admitted past a limit to let its command
	// finish; the caller should wrap up.
	OverLimit bool `json:"over_limit,omitempty"`
}
type DurableStore interface {
	Store
	Reservation(context.Context, Request) (*Reservation, error)
	SaveReservation(context.Context, Reservation) error
	BeginAdmission(context.Context, Request) error
	AbandonedAdmissions(context.Context, int) ([]Request, error)
	AdmissionRecovered(context.Context, Request) error
}
type Service struct {
	Adapter Adapter
	Store   DurableStore
	// Gate refuses AI admission before billing is asked, for checks only the
	// server can make (an unverified email). It returns a *Denial.
	Gate func(context.Context, Request) error
}

func keyID(account, key string) string {
	digest := sha256.Sum256([]byte(account + "\x00" + key))
	return hex.EncodeToString(digest[:])
}
func (s Service) Reserve(ctx context.Context, req Request) (*Reservation, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := validate("reserve", req); err != nil {
		return nil, err
	}
	if !s.Adapter.Enabled() {
		return &Reservation{ID: "disabled:" + keyID(req.AccountID, req.Key), Admission: req}, nil
	}
	if s.Store == nil {
		return nil, errors.New("billing admission storage is required")
	}
	if s.Gate != nil {
		if err := s.Gate(ctx, req); err != nil {
			return nil, err
		}
	}
	prior, err := s.Store.Reservation(ctx, req)
	if err != nil {
		return nil, err
	}
	if prior != nil {
		return prior, nil
	}
	if err = s.Store.BeginAdmission(ctx, req); err != nil {
		return nil, err
	}
	decision, err := s.Adapter.Do(ctx, "reserve", req)
	if err != nil {
		return nil, err
	}
	var admitted struct {
		OverLimit bool `json:"over_limit"`
	}
	_ = json.Unmarshal(decision.Summary, &admitted)
	reservation := Reservation{ID: decision.ReservationID, Admission: req, OverLimit: admitted.OverLimit}
	if err = s.Store.SaveReservation(ctx, reservation); err != nil {
		return nil, err
	}
	return &reservation, nil
}
func (s Service) Complete(ctx context.Context, action string, reservation *Reservation, key string, usage Usage, reason string) error {
	if reservation == nil {
		return ErrInvalid
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	req := reservation.Admission
	req.ReservationID = reservation.ID
	req.Key = key
	req.Usage = usage
	req.Reason = reason
	return (Reliable{Adapter: s.Adapter, Store: s.Store}).Submit(ctx, action, req)
}

// RecoverAdmissionsBatch reports claimed rows so an event worker can distinguish
// progress from another replica holding the ready rows' locks.
func (s Service) RecoverAdmissionsBatch(ctx context.Context, limit int) (int, error) {
	if !s.Adapter.Enabled() {
		return 0, nil
	}
	if s.Store == nil {
		return 0, ErrUnavailable
	}
	pending, err := s.Store.AbandonedAdmissions(ctx, limit)
	if err != nil {
		return 0, err
	}
	count := len(pending) // The store persisted a retry deadline while claiming these rows.
	for _, req := range pending {
		d, err := s.Adapter.Do(ctx, "reserve", req)
		if errors.Is(err, ErrDenied) {
			if err = s.Store.AdmissionRecovered(ctx, req); err != nil {
				return count, err
			}
			continue
		}
		if err != nil {
			continue
		}
		req.ReservationID = d.ReservationID
		original := req
		req.Key = "abandoned-release:" + keyID(req.AccountID, req.Key)
		if err = (Reliable{Adapter: s.Adapter, Store: s.Store}).Submit(ctx, "release", req); err != nil {
			return count, err
		}
		req = original
		if err = s.Store.AdmissionRecovered(ctx, req); err != nil {
			return count, err
		}
	}
	return count, nil
}

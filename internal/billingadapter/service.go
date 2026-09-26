package billingadapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
)

// Reservation carries authenticated identity with the opaque provider ID. It is
// persisted before a provider call, so settlement never needs fresh admission.
type Reservation struct {
	ID        string  `json:"id"`
	Admission Request `json:"admission"`
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
	reservation := Reservation{ID: decision.ReservationID, Admission: req}
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

// RecoverAdmissions completes only attempts that could not reach provider work.
// It replays the original key, then durably releases the opaque hold.
func (s Service) RecoverAdmissions(ctx context.Context, limit int) error {
	if !s.Adapter.Enabled() {
		return nil
	}
	if s.Store == nil {
		return ErrUnavailable
	}
	pending, err := s.Store.AbandonedAdmissions(ctx, limit)
	if err != nil {
		return err
	}
	for _, req := range pending {
		d, err := s.Adapter.Do(ctx, "reserve", req)
		if errors.Is(err, ErrDenied) {
			if err = s.Store.AdmissionRecovered(ctx, req); err != nil {
				return err
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
			return err
		}
		req = original
		if err = s.Store.AdmissionRecovered(ctx, req); err != nil {
			return err
		}
	}
	return nil
}

package library

import (
	"context"
	"sync"

	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/billingadapter"
)

// Meter belongs to one Library job/request, never a shared analyzer. Each actual
// provider attempt gets its own reservation, including fallbacks and embeddings.
// Only raw measurements leave the public server; pricing belongs to the adapter.
type Meter struct {
	Service              *billingadapter.Service
	Account, OperationID string
	mu                   sync.Mutex
	err                  error
}

func (m *Meter) Err() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.err
}

func (m *Meter) fail(err error) error {
	if err != nil {
		m.mu.Lock()
		if m.err == nil {
			m.err = err
		}
		m.mu.Unlock()
	}
	return err
}

type Attempt struct {
	meter       *Meter
	reservation *billingadapter.Reservation
}

func (m *Meter) Begin(ctx context.Context, operation, model string, units map[string]int64) (*Attempt, error) {
	if m == nil {
		return nil, nil
	}
	if err := m.Err(); err != nil {
		return nil, err
	}
	if m.Service == nil {
		return nil, m.fail(billingadapter.ErrUnavailable)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Repeated work is a new physical provider attempt. Re-delivery of its
	// settlement uses this same persisted key, never another reservation.
	r, err := m.Service.Reserve(ctx, billingadapter.Request{Version: 1, AccountID: m.Account, Operation: operation, OperationID: m.OperationID, Key: "library-attempt:" + uuid.NewString(), Usage: billingadapter.Usage{Provider: "vercel_ai_gateway", Model: model, Units: units, Estimated: true}})
	if err != nil {
		return nil, m.fail(err)
	}
	return &Attempt{meter: m, reservation: r}, nil
}

// Finish persists completion even after interruption. A successful response
// with no usable usage report retains its explicitly estimated reservation;
// failed calls release their hold. A failed durable write stops further work.
func (a *Attempt) Finish(ctx context.Context, success bool, units map[string]int64, estimated bool) error {
	if a == nil {
		return nil
	}
	r := a.reservation
	action := "release"
	u := billingadapter.Usage{}
	if success {
		action = "settle"
		if estimated {
			units = r.Admission.Usage.Units
		}
		u = billingadapter.Usage{Provider: r.Admission.Usage.Provider, Model: r.Admission.Usage.Model, Units: units, Estimated: estimated}
	}
	return a.meter.fail(a.meter.Service.Complete(ctx, action, r, r.Admission.Key+":"+action, u, ""))
}

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	agent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/billingadapter"
)

type voiceHold struct {
	reservation *billingadapter.Reservation
	closed      bool
}

// Each increment has its own durable journal entry and idempotency key. This
// uses ordinary reservations, so crash recovery needs no new billing protocol.
type voiceReservations struct {
	operation, user string
 model string
	holds           []*voiceHold
	accounted       agent.RealtimeVoiceUsage
	inputSeconds    int
	estimate        func(context.Context, billingadapter.Request) (map[string]int64, error)
	disabled        bool
	reserve         func(context.Context, billingadapter.Request) (*billingadapter.Reservation, error)
	record          func(context.Context, *billingadapter.Reservation, string, map[string]int64) error
	complete        func(context.Context, string, *billingadapter.Reservation, string, billingadapter.Usage, string) error
}

func (b *voiceReservations) admit(ctx context.Context, phase string, units agent.RealtimeVoiceUsage) error {
	key := fmt.Sprintf("%s:%s:%d", b.operation, phase, len(b.holds))
	req := billingadapter.Request{CommandID: b.operation, Version: 1, AccountID: b.user, Operation: "agent.voice.realtime", OperationID: key, Key: key, Usage: billingadapter.Usage{Provider: "openai", Model: b.modelID(), Units: units, Estimated: true}}
	if b.estimate != nil {
		measured, err := b.estimate(ctx, req)
		if err != nil {
			return err
		}
		req.Usage.Units = measured
	}
	r, err := b.reserve(ctx, req)
	if err != nil {
		return err
	}
	h := &voiceHold{reservation: r}
	b.holds = append(b.holds, h)
	if err = b.record(ctx, r, "active", map[string]int64{}); err != nil {
		// The reservation is durable even if its voice journal write failed.
		if b.complete(ctx, "release", r, r.Admission.Key+":release", billingadapter.Usage{Provider: "openai", Model: b.modelID(), Units: agent.RealtimeVoiceUsage{}}, "") == nil {
			h.closed = true
			_ = b.record(ctx, r, "closed", agent.RealtimeVoiceUsage{})
		}
		return err
	}
	return nil
}
func (b *voiceReservations) input(ctx context.Context, bytes int) error {
	seconds := ((bytes + 24000*2*5 - 1) / (24000 * 2 * 5)) * 5
	if seconds <= b.inputSeconds {
		return nil
	}
	if seconds > 60 {
		return errors.New("voice input exceeded its limit")
	}
	delta := seconds - b.inputSeconds
	if err := b.admit(ctx, "input", agent.RealtimeVoiceUsage{"transcription_ms": int64(delta * 1000)}); err != nil {
		return err
	}
	b.inputSeconds = seconds
	return nil
}

// Allocate measured counters over the open holds, without converting prices or
// silently capping overspend. Cached input is a subset of its input modality.
func (b *voiceReservations) portions(usage agent.RealtimeVoiceUsage) (map[*voiceHold]agent.RealtimeVoiceUsage, error) {
	left := agent.RealtimeVoiceUsage{}
	for k, n := range usage {
		left[k] = n - b.accounted[k]
		if left[k] < 0 {
			return nil, errors.New("voice usage went backwards")
		}
	}
	parts := map[*voiceHold]agent.RealtimeVoiceUsage{}
	for _, h := range b.holds {
		if h.closed {
			continue
		}
		part := agent.RealtimeVoiceUsage{}
		if b.disabled {
			for k, n := range left {
				part[k] = n
				left[k] = 0
			}
			parts[h] = part
			continue
		}
		for k, cap := range h.reservation.Admission.Usage.Units {
			n := left[k]
			if n > cap {
				n = cap
			}
			if n > 0 {
				part[k] = n
				left[k] -= n
			}
		}
		for _, kind := range []string{"text", "audio"} {
			k := "cached_input_" + kind + "_tokens"
			n := left[k]
			if cap := part["input_"+kind+"_tokens"]; n > cap {
				n = cap
			}
			if n > 0 {
				part[k] = n
				left[k] -= n
			}
		}
		parts[h] = part
	}
	for _, n := range left {
		if n != 0 {
			return nil, errors.New("voice usage exceeded its reservation")
		}
	}
	return parts, nil
}
func (b *voiceReservations) checkpoint(ctx context.Context, state string, usage agent.RealtimeVoiceUsage) error {
	parts, err := b.portions(usage)
	if err != nil {
		state = "reconcile"
	}
	for _, h := range b.holds {
		if h.closed {
			continue
		}
		part := parts[h]
		if part == nil {
			part = agent.RealtimeVoiceUsage{}
		}
		if e := b.record(ctx, h.reservation, state, part); e != nil {
			return e
		}
	}
	return err
}
func (b *voiceReservations) finish(ctx context.Context, usage agent.RealtimeVoiceUsage) error {
	parts, err := b.portions(usage)
	if err != nil {
		_ = b.checkpoint(ctx, "reconcile", usage)
		return err
	}
	if b.accounted == nil {
		b.accounted = agent.RealtimeVoiceUsage{}
	}
	for _, h := range b.holds {
		if h.closed {
			continue
		}
		part := parts[h]
		if err = b.record(ctx, h.reservation, "settlement_pending", part); err != nil {
			return err
		}
		action := "release"
		for _, n := range part {
			if n > 0 {
				action = "settle"
				break
			}
		}
		r := h.reservation
		if err = b.complete(ctx, action, r, r.Admission.Key+":"+action, billingadapter.Usage{Provider: "openai", Model: b.modelID(), Units: part}, ""); err != nil {
			return err
		}
		if err = b.record(ctx, r, "closed", part); err != nil {
			return err
		}
		h.closed = true
		b.accounted.Add(part)
	}
	return nil
}

// Estimation and tokenization policy remain behind the billing adapter. Reserve
// repeats admission atomically; the preview cannot authorize provider work.
func voiceBillingEstimate(service *billingadapter.Service) func(context.Context, billingadapter.Request) (map[string]int64, error) {
	if !service.Adapter.Enabled() {
		return nil
	}
	return func(ctx context.Context, r billingadapter.Request) (map[string]int64, error) {
		d, err := service.Adapter.Do(ctx, "check", r)
		if err != nil {
			return nil, err
		}
		if !d.Allowed {
			return nil, billingadapter.ErrDenied
		}
		var summary struct {
			Units map[string]int64 `json:"admission_units"`
		}
		if json.Unmarshal(d.Summary, &summary) != nil || len(summary.Units) == 0 {
			return nil, billingadapter.ErrUnavailable
		}
		return summary.Units, nil
	}
}

func (b *voiceReservations) modelID() string { if b.model != "" { return b.model }; return agent.AgentRealtimeModel }

func voiceProviderModel(provider voiceProvider) string { if p, ok := provider.(interface{ ModelID() string }); ok { return p.ModelID() }; return agent.AgentRealtimeModel }

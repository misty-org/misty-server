package api

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	agent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/billingadapter"
)

type budgetFixture struct {
	budget       *voiceReservations
	requests     []billingadapter.Request
	completions  []voiceSettlement
	states       []string
	deny         bool
	failComplete bool
}

func newBudgetFixture() *budgetFixture {
	f := &budgetFixture{}
	f.budget = &voiceReservations{operation: "fixture", user: "owner",
		// Fake private billing estimates; production receives these via its adapter.
		estimate: func(_ context.Context, r billingadapter.Request) (map[string]int64, error) {
			if ms, ok := r.Usage.Units["transcription_ms"]; ok {
				return map[string]int64{"transcription_input_tokens": ms / 10, "transcription_output_tokens": ms * 7 / 100}, nil
			}
			if _, ok := r.Usage.Units["context_bytes"]; ok {
				return map[string]int64{"input_text_tokens": 4096, "input_audio_tokens": 4096, "output_text_tokens": 2048, "output_audio_tokens": 2048}, nil
			}
			return r.Usage.Units, nil
		},
		reserve: func(_ context.Context, r billingadapter.Request) (*billingadapter.Reservation, error) {
			if f.deny {
				return nil, billingadapter.ErrDenied
			}
			f.requests = append(f.requests, r)
			return &billingadapter.Reservation{ID: fmt.Sprint(len(f.requests)), Admission: r}, nil
		},
		record: func(_ context.Context, _ *billingadapter.Reservation, state string, _ map[string]int64) error {
			f.states = append(f.states, state)
			return nil
		},
		complete: func(_ context.Context, action string, _ *billingadapter.Reservation, _ string, u billingadapter.Usage, _ string) error {
			if f.failComplete {
				f.failComplete = false
				return errors.New("temporary failure")
			}
			f.completions = append(f.completions, voiceSettlement{action, u.Units})
			return nil
		},
	}
	return f
}
func TestVoiceInputReservationsExtendThenSettleBeforeSpeech(t *testing.T) {
	ctx := context.Background()
	f := newBudgetFixture()
	for _, bytes := range []int{1, 240000, 240001} {
		if err := f.budget.input(ctx, bytes); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.requests) != 2 {
		t.Fatal(f.requests)
	}
	for _, r := range f.requests {
		if !reflect.DeepEqual(r.Usage.Units, map[string]int64{"transcription_input_tokens": 500, "transcription_output_tokens": 350}) {
			t.Fatal(r)
		}
	}
	if f.requests[0].OperationID == f.requests[1].OperationID {
		t.Fatal("increments share journal identity")
	}
	f.deny = true
	if err := f.budget.input(ctx, 480001); !errors.Is(err, billingadapter.ErrDenied) {
		t.Fatal(err)
	}
	if f.budget.inputSeconds != 10 {
		t.Fatal("failed increment advanced recording allowance")
	}
	f.deny = false
	usage := agent.RealtimeVoiceUsage{"transcription_input_tokens": 550, "transcription_output_tokens": 30}
	if err := f.budget.finish(ctx, usage); err != nil {
		t.Fatal(err)
	}
	if len(f.completions) != 2 || f.completions[0].Usage["transcription_input_tokens"] != 500 || f.completions[1].Usage["transcription_input_tokens"] != 50 {
		t.Fatal(f.completions)
	}
	for _, h := range f.budget.holds {
		if !h.closed {
			t.Fatal("transcription hold survived settlement")
		}
	}
	estimate := agent.RealtimeVoiceUsage{"context_bytes": 32, "speech_bytes": 6, "retained_audio_tokens": 550, "output_token_limit": 2048}
	if err := f.budget.admit(ctx, "speech", estimate); err != nil {
		t.Fatal(err)
	}
	usage.Add(agent.RealtimeVoiceUsage{"input_text_tokens": 20, "cached_input_text_tokens": 10, "input_audio_tokens": 550, "output_text_tokens": 4, "output_audio_tokens": 25})
	if err := f.budget.finish(ctx, usage); err != nil {
		t.Fatal(err)
	}
	if len(f.completions) != 3 || f.completions[2].Usage["transcription_input_tokens"] != 0 || f.completions[2].Usage["cached_input_text_tokens"] != 10 {
		t.Fatal(f.completions)
	}
	if err := f.budget.finish(ctx, usage); err != nil || len(f.completions) != 3 {
		t.Fatal("duplicate settlement", err)
	}
}
func TestVoiceBudgetReleaseAndRecovery(t *testing.T) {
	ctx := context.Background()
	f := newBudgetFixture()
	if err := f.budget.input(ctx, 1); err != nil {
		t.Fatal(err)
	}
	f.failComplete = true
	if err := f.budget.finish(ctx, agent.RealtimeVoiceUsage{}); err == nil {
		t.Fatal("expected temporary failure")
	}
	if f.states[len(f.states)-1] != "settlement_pending" {
		t.Fatal(f.states)
	}
	if err := f.budget.finish(ctx, agent.RealtimeVoiceUsage{}); err != nil {
		t.Fatal(err)
	}
	if len(f.completions) != 1 || f.completions[0].Action != "release" {
		t.Fatal(f.completions)
	}
}
func TestVoiceOverReservationUsageRequiresReconciliation(t *testing.T) {
	f := newBudgetFixture()
	ctx := context.Background()
	if err := f.budget.input(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := f.budget.finish(ctx, agent.RealtimeVoiceUsage{"transcription_input_tokens": 501}); err == nil {
		t.Fatal("overspend silently capped")
	}
	if len(f.completions) != 0 || f.states[len(f.states)-1] != "reconcile" {
		t.Fatal(f.states, f.completions)
	}
}


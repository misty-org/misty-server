package billingadapter

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"testing"
)

// The harness starts the real billing executable with its migration and runtime
// roles. Ordinary unit-test runs remain independent of the sibling checkout.
func TestLiveBillingContract(t *testing.T) {
	endpoint := os.Getenv("MISTY_BILLING_CONTRACT_URL")
	if endpoint == "" {
		t.Skip("run scripts/test-billing-contract.sh for the live adapter contract")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" {
		t.Fatal("contract tests require a disposable loopback billing service")
	}
	config := Config{Mode: "http", URL: endpoint, Secret: os.Getenv("MISTY_BILLING_CONTRACT_SECRET"), Required: true, AllowLoopbackHTTP: true}
	adapter, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	request := Request{Version: 1, AccountID: "contract-account", Operation: "customer.summary", OperationID: "summary", Key: "summary"}
	summary, err := adapter.Do(ctx, "summary", request)
	if err != nil || !summary.Allowed || !json.Valid(summary.Summary) {
		t.Fatalf("summary: %+v %v", summary, err)
	}
	request.Operation, request.OperationID, request.Key = "agent", "contract-run", "reserve"
	request.Usage = Usage{Model: "gpt-5.6-luna", Units: map[string]int64{"input_tokens": 10}}
	reservation, err := adapter.Do(ctx, "reserve", request)
	if err != nil || reservation.ReservationID == "" {
		t.Fatalf("reserve: %+v %v", reservation, err)
	}
	replay, err := adapter.Do(ctx, "reserve", request)
	if err != nil || replay.ReservationID != reservation.ReservationID {
		t.Fatalf("idempotent reserve: %+v %v", replay, err)
	}
	changed := request
	changed.OperationID = "different-run"
	if _, err = adapter.Do(ctx, "reserve", changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting replay: %v", err)
	}
	request.ReservationID, request.Key = reservation.ReservationID, "settle"
	for range 2 {
		if _, err = adapter.Do(ctx, "settle", request); err != nil {
			t.Fatalf("idempotent settle: %v", err)
		}
	}
	request.Key = "settle-twice"
	if _, err = adapter.Do(ctx, "settle", request); !errors.Is(err, ErrConflict) {
		t.Fatalf("double settlement: %v", err)
	}
	request.AccountID, request.Key = "another-account", "cross-account"
	if _, err = adapter.Do(ctx, "settle", request); err == nil {
		t.Fatal("cross-account settlement accepted")
	}
	testLiveLimitsContract(t, ctx, adapter)
	config.Secret = "incorrect-contract-signing-key-32-bytes"
	wrong, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = wrong.Do(ctx, "summary", Request{Version: 1, AccountID: "contract-account", Operation: "customer.summary", OperationID: "summary", Key: "summary"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("invalid signature accepted: %v", err)
	}
}

// testLiveLimitsContract checks what pricing v2 added: refusals that name their
// limit, the grace for a running command, web search, unpriced models and
// referrals. Basic has a 450k weekly limit, a 135k 5-hour window (27k grace)
// and a 112.5k per-command cap; built-in prices charge gpt-5.6-luna $1/M input.
func testLiveLimitsContract(t *testing.T, ctx context.Context, adapter Adapter) {
	t.Helper()
	reserve := func(command, key string, units int64) (Decision, error) {
		return adapter.Do(ctx, "reserve", Request{Version: 1, AccountID: "limits-account", Operation: "agent.model", OperationID: command + ":" + key, Key: command + ":" + key, CommandID: command,
			Usage: Usage{Model: "gpt-5.6-luna", Units: map[string]int64{"input_tokens": units}, Estimated: true}})
	}
	if _, err := reserve("a", "1", 100_000); err != nil {
		t.Fatalf("first command refused: %v", err)
	}
	if _, err := reserve("d", "1", 30_000); err != nil {
		t.Fatalf("second command inside the window refused: %v", err)
	}
	_, err := reserve("b", "1", 10_000)
	var denial *Denial
	if !errors.As(err, &denial) || denial.Limit != "window" || denial.ResetsAt == nil {
		t.Fatalf("a new command past the 5-hour window: %v %+v", err, denial)
	}
	over, err := reserve("a", "2", 10_000)
	var admitted struct {
		OverLimit bool `json:"over_limit"`
	}
	if err != nil || json.Unmarshal(over.Summary, &admitted) != nil || !admitted.OverLimit {
		t.Fatalf("a running command could not finish on its grace: %v %s", err, over.Summary)
	}
	if _, err = reserve("d", "2", 30_000); !errors.As(err, &denial) || denial.Limit != "window" {
		t.Fatalf("a command went past its grace: %v", err)
	}
	if _, err = adapter.Do(ctx, "reserve", Request{Version: 1, AccountID: "model-account", Operation: "agent.model", OperationID: "m", Key: "m",
		Usage: Usage{Model: "someone/unpriced-model", Units: map[string]int64{"input_tokens": 10}, Estimated: true}}); !errors.As(err, &denial) || denial.Limit != "model" {
		t.Fatalf("an unpriced model was admitted: %v", err)
	}
	search := Request{Version: 1, AccountID: "search-account", Operation: "web.search", OperationID: "s", Key: "s", CommandID: "s",
		Usage: Usage{Provider: "parallel", Model: "google/gemini-2.5-flash-lite", Units: map[string]int64{"requests": 1, "input_bytes": 600, "output_tokens": 400}, Estimated: true}}
	held, err := adapter.Do(ctx, "reserve", search)
	if err != nil || held.ReservationID == "" {
		t.Fatalf("web search reservation: %v", err)
	}
	search.ReservationID, search.Key = held.ReservationID, "s:settle"
	search.Usage = Usage{Provider: "parallel", Model: "google/gemini-2.5-flash-lite", Units: map[string]int64{"requests": 1}}
	if _, err = adapter.Do(ctx, "settle", search); err != nil {
		t.Fatalf("web search settlement: %v", err)
	}
	// The cheaper voice pipeline: admit from native measurements, settle speech-to-text
	// milliseconds, text-model tokens and speech characters.
	voice := func(key string, units map[string]int64, estimated bool, reservation string) (Decision, error) {
		action := "reserve"
		if reservation != "" {
			action = "settle"
		}
		return adapter.Do(ctx, action, Request{Version: 1, AccountID: "voice-account", Operation: "agent.voice.realtime", OperationID: key, Key: key + ":" + action, CommandID: "voice", ReservationID: reservation,
			Usage: Usage{Provider: "openai", Model: "misty/voice-pipeline", Units: units, Estimated: estimated}})
	}
	for _, step := range []struct {
		key             string
		estimate, usage map[string]int64
	}{
		{"listen", map[string]int64{"transcription_ms": 5000}, map[string]int64{"stt_ms": 4200}},
		{"reply", map[string]int64{"context_bytes": 9000, "audio_pcm_bytes": 200_000, "output_token_limit": 768}, map[string]int64{"input_tokens": 6000, "output_tokens": 120, "tts_characters": 300}},
	} {
		held, err := voice(step.key, step.estimate, true, "")
		if err != nil || held.ReservationID == "" {
			t.Fatalf("voice pipeline %s admission: %v", step.key, err)
		}
		if _, err = voice(step.key, step.usage, false, held.ReservationID); err != nil {
			t.Fatalf("voice pipeline %s settlement: %v", step.key, err)
		}
	}
	refer := func(friend string, referral Referral) (Decision, error) {
		return adapter.Do(ctx, "referral", Request{Version: 1, AccountID: friend, Operation: "customer.referral", OperationID: "referral", Key: "referral", Referral: &referral})
	}
	if d, err := refer("friend-account", Referral{ReferrerID: "limits-account", EmailVerified: true}); err != nil || !d.Allowed {
		t.Fatalf("referral attribution: %+v %v", d, err)
	}
	if _, err = refer("sock-account", Referral{ReferrerID: "limits-account", EmailVerified: true, SameNetwork: true}); !errors.Is(err, ErrDenied) {
		t.Fatalf("same-network referral accepted: %v", err)
	}
	summary, err := adapter.Do(ctx, "summary", Request{Version: 1, AccountID: "limits-account", Operation: "customer.summary", OperationID: "summary", Key: "summary"})
	var view struct {
		AI struct {
			Limits map[string]struct {
				Limit int64 `json:"limit"`
			} `json:"limits"`
		} `json:"ai"`
		Referrals struct {
			Pending int64 `json:"pending"`
		} `json:"referrals"`
		TrialEligible bool `json:"trial_eligible"`
	}
	if err != nil || json.Unmarshal(summary.Summary, &view) != nil || view.AI.Limits["window"].Limit != 135_000 || view.AI.Limits["monthly"].Limit != 2_000_000 || view.Referrals.Pending != 1 || view.TrialEligible {
		t.Fatalf("summary limits and referrals: %v %s", err, summary.Summary)
	}
}

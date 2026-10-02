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
	config.Secret = "incorrect-contract-signing-key-32-bytes"
	wrong, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = wrong.Do(ctx, "summary", Request{Version: 1, AccountID: "contract-account", Operation: "customer.summary", OperationID: "summary", Key: "summary"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("invalid signature accepted: %v", err)
	}
}

package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/misty-org/misty-server/internal/platform/db"
)

// Agent runs work on the person's desktop (docs/agents/runtime.md). The
// desktop claims the deliveries for runs it keeps, hands each to its own
// runtime and reports how it went. The server's checks and bookkeeping are in
// agent_runtime_delivery.go.

var (
	errAgentRuntimeOnDevice = errors.New("agent runs work on the desktop")
	// errAgentRunOnOtherDevice leaves the delivery for the device the run is pinned to.
	errAgentRunOnOtherDevice = errors.New("another device runs this work")
)

// agentRunKey is the key a desktop runtime signs one run's control calls with.
// It opens that run only, and the server derives it again instead of storing it.
func agentRunKey(secret []byte, runID string) []byte {
	if len(secret) == 0 || runID == "" {
		return nil
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("misty-run-key\x00"))
	mac.Write([]byte(runID))
	return mac.Sum(nil)
}

type deviceAgentDelivery struct {
	ID      string `json:"id"`
	LeaseID string `json:"lease_id"`
	agentDeliveryMessage
	// A start carries what the runtime needs to work on the run: its key, the
	// engine it was admitted for and the receipt the server pinned.
	RunKey          string `json:"run_key,omitempty"`
	AdapterVersion  string `json:"adapter_version,omitempty"`
	ReceiptCallback string `json:"receipt_callback,omitempty"`
}

// agentRuntimeContextRequest is a run's context request. A desktop runtime says
// which of the device's models the run thinks on, and which tools it runs itself.
type agentRuntimeContextRequest struct {
	RuntimeRunID    string   `json:"runtime_run_id"`
	ModelID         string   `json:"model_id"`
	ReasoningEffort string   `json:"reasoning_effort"`
	DeviceTools     []string `json:"device_tools"`
}

// webSearchTool runs in the desktop runtime on a search key the person keeps on
// that device (docs/agents/runtime.md). The server never runs it, holds no search
// key and counts no searches; it only describes the tool in a run's prompt when the
// device says it has it.
const webSearchTool = "web.search"

// webFetchTool also runs in the desktop runtime, on every run: an agent run is always
// on the computer that started it, so the server never reads a page.
const webFetchTool = "web.fetch"

// deviceTools are the tools the desktop runs itself for this run, of those the
// server knows to describe.
func (b agentRuntimeContextRequest) deviceTools() []string {
	var tools []string
	for _, name := range []string{webSearchTool, webFetchTool} {
		if slices.Contains(b.DeviceTools, name) {
			tools = append(tools, name)
		}
	}
	return tools
}

// deviceModel is the model the desktop reported, when it reported a usable one.
func (b agentRuntimeContextRequest) deviceModel() (string, string, bool) {
	model := strings.TrimSpace(b.ModelID)
	if model == "" || len(model) > 200 || !strings.Contains(model, "/") || strings.ContainsAny(model, " \t\r\n") {
		return "", "", false
	}
	return model, strings.TrimSpace(b.ReasoningEffort), true
}

const maxRuntimeClaimWait = 25 * time.Second

var runtimeClaimWaits deviceClaimWaits

// ClaimDeviceAgentRuntimeDeliveries hands a desktop the runs it starts, cancels
// and resumes. waitMs holds the claim open until one is queued (at most 25 s).
func (s *SpacesService) ClaimDeviceAgentRuntimeDeliveries() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := sessionUserID(r, s.database)
		if err != nil || userID == "" {
			http.Error(w, "not authenticated", http.StatusUnauthorized)
			return
		}
		if _, ok := requestDevice(r); !ok {
			http.Error(w, "device signature required", http.StatusUnauthorized)
			return
		}
		deviceID := chi.URLParam(r, "deviceID")
		body := struct {
			WaitMs int `json:"waitMs"`
		}{}
		if r.ContentLength != 0 && decodeAIJSON(w, r, &body) != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		wait := time.Duration(body.WaitMs) * time.Millisecond
		if body.WaitMs < 0 || wait > maxRuntimeClaimWait {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if !s.agentRuntime.Enabled() {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		holder := userID + ":" + deviceID
		if wait > 0 && !runtimeClaimWaits.acquire(holder) {
			wait = 0
		} else if wait > 0 {
			defer runtimeClaimWaits.release(holder)
		}
		claimed, err := s.claimRuntimeDeliveriesWithin(r.Context(), userID, deviceID, wait)
		if err != nil {
			if r.Context().Err() == nil {
				writeAgentError(w, err)
			}
			return
		}
		deliveries := []deviceAgentDelivery{}
		for _, delivery := range claimed {
			if item, ok := s.deviceAgentDelivery(r.Context(), delivery, deviceID); ok {
				deliveries = append(deliveries, item)
			}
		}
		if len(deliveries) == 0 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deliveries": deliveries})
	}
}

// claimRuntimeDeliveriesWithin claims the device's deliveries, waiting up to wait
// for one to be queued. Queued deliveries arrive on the "runtime" account topic.
func (s *SpacesService) claimRuntimeDeliveriesWithin(ctx context.Context, userID, deviceID string, wait time.Duration) ([]db.AgentRuntimeDelivery, error) {
	claim := func() ([]db.AgentRuntimeDelivery, error) {
		return s.database.ClaimDeviceAgentRuntimeDeliveries(ctx, userID, deviceID, 10)
	}
	if wait <= 0 {
		return claim()
	}
	// Subscribe before the first claim so a delivery queued in between still wakes it.
	events, stop, err := s.database.SubscribeAccountEvents(ctx, userID)
	if err != nil {
		return claim()
	}
	defer stop()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	// A delivery whose retry came due or whose lease expired sends no event.
	recheck := time.NewTicker(deviceClaimRecheck)
	defer recheck.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		claimed, err := claim()
		if err != nil || len(claimed) > 0 {
			return claimed, err
		}
	wait:
		for {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-timer.C:
				return nil, nil
			case <-recheck.C:
				break wait
			case event, open := <-events:
				if !open {
					return nil, nil
				}
				if event.Topic == "runtime" || event.Topic == "reset" {
					break wait
				}
			}
		}
	}
}

// deviceAgentDelivery runs the server's checks for one claimed delivery. A
// delivery with nothing left for the runtime to do is settled here and not
// handed over; a start is pinned to this device first.
func (s *SpacesService) deviceAgentDelivery(ctx context.Context, delivery db.AgentRuntimeDelivery, deviceID string) (deviceAgentDelivery, bool) {
	message, err := s.planAgentDelivery(ctx, delivery)
	item := deviceAgentDelivery{ID: delivery.ID, LeaseID: delivery.LeaseID, agentDeliveryMessage: message}
	if err == nil && !message.Skip && delivery.Operation == "invocation.start" {
		endpoint := db.DeviceRuntimeEndpoint(deviceID)
		pin, bindErr := s.database.BindAgentRuntime(ctx, delivery.RunID, endpoint, endpoint)
		switch {
		case bindErr != nil:
			err = bindErr
		case pin.Endpoint != endpoint:
			err = errAgentRunOnOtherDevice
		case pin.AdapterVersion != db.BetaAgentRuntimeAdapter:
			err = db.ErrSpaceInvalid
		default:
			item.RunKey = base64.StdEncoding.EncodeToString(agentRunKey(s.agentRuntime.secret, delivery.RunID))
			item.AdapterVersion, item.ReceiptCallback = pin.AdapterVersion, pin.CallbackEndpoint
		}
	}
	if err != nil || message.Skip {
		_, _ = s.settleAgentDelivery(ctx, delivery, agentDeliveryOutcome{Err: err, Skipped: err == nil})
		return item, false
	}
	return item, true
}

// FinishDeviceAgentRuntimeDelivery records what the desktop's runtime did with a
// delivery it claimed.
func (s *SpacesService) FinishDeviceAgentRuntimeDelivery() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := sessionUserID(r, s.database)
		if err != nil || userID == "" {
			http.Error(w, "not authenticated", http.StatusUnauthorized)
			return
		}
		if _, ok := requestDevice(r); !ok {
			http.Error(w, "device signature required", http.StatusUnauthorized)
			return
		}
		var body struct {
			LeaseID      string `json:"lease_id"`
			RuntimeRunID string `json:"runtime_run_id"`
			Status       string `json:"status"`
			Error        string `json:"error"`
		}
		if decodeAIJSON(w, r, &body) != nil || body.LeaseID == "" || len(body.RuntimeRunID) > 512 || len(body.Error) > 1000 {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		delivery, err := s.database.LeasedAgentRuntimeDelivery(r.Context(), userID, chi.URLParam(r, "deliveryID"), body.LeaseID)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		outcome := agentDeliveryOutcome{RuntimeRunID: body.RuntimeRunID, Status: body.Status}
		switch body.Error {
		case "":
		case "workflow_start_unconfirmed":
			outcome.Err = errAgentRuntimeStartUnconfirmed
		case "harness_version_unavailable":
			outcome.Err = db.ErrSpaceInvalid
		default:
			outcome.Err = errors.New(body.Error)
		}
		done, err := s.settleAgentDelivery(r.Context(), delivery, outcome)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"done": done})
	}
}

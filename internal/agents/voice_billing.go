package agent

import (
	"context"

	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/billingadapter"
)

type voiceBillingError struct{ error }

func (e *voiceBillingError) Unwrap() error { return e.error }

// Each fallback is separately admitted with its actual model identity. Billing
// denial or failure must not trigger another paid provider attempt.
func (a *SmartLibraryAnalyzer) TranscribeAgentVoiceWithBilling(ctx context.Context, audio []byte, mimeType string, durationMS int64, billing *billingadapter.Service, account string) (string, string, AgentVoiceUsage, error) {
	operation := "agent-voice:" + uuid.NewString()
	return transcribeAgentVoice(ctx, audio, durationMS, func(model string) ([]MediaTranscriptSegment, ModelUsage, string, int64, error) {
		key := operation + ":" + model
		r, err := billing.Reserve(ctx, billingadapter.Request{Version: 1, AccountID: account, Operation: "agent.voice.transcription", OperationID: operation, Key: key, Usage: billingadapter.Usage{Provider: "openai", Model: model, Units: map[string]int64{"audio_ms": 60000}, Estimated: true}})
		if err != nil {
			return nil, ModelUsage{}, "", 0, &voiceBillingError{err}
		}
		segments, usage, language, actual, providerErr := a.transcribeMediaWithModel(ctx, audio, mimeType, durationMS, model)
		if providerErr != nil {
			if err = billing.Complete(ctx, "release", r, key+":release", billingadapter.Usage{}, "provider_failed"); err != nil {
				return nil, usage, "", 0, &voiceBillingError{err}
			}
			return nil, usage, "", 0, providerErr
		}
		if actual <= 0 {
			actual = durationMS
		}
		if err = billing.Complete(ctx, "settle", r, key+":settle", billingadapter.Usage{Provider: "openai", Model: model, Units: map[string]int64{"audio_ms": actual}}, ""); err != nil {
			return nil, usage, "", 0, &voiceBillingError{err}
		}
		return segments, usage, language, actual, nil
	})
}

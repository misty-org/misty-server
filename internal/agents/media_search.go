package agent

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/kannachi323/misty/server/internal/modelruntime"
	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
)

const (
	MediaSearchTranscriptionModel         = "xai/grok-stt"
	MediaSearchTranscriptionFallbackModel = "openai/whisper-1"
	AgentSpeechModel                      = "openai/gpt-4o-mini-tts"
)

type MediaTranscriptSegment struct {
	StartMS int64  `json:"startMs"`
	EndMS   int64  `json:"endMs"`
	Text    string `json:"text"`
}

type AgentVoiceUsage struct {
	Model      string
	DurationMS int64
}

func transcribeAgentVoiceModels(ctx context.Context, audio []byte, durationMS int64, model, fallback string, call func(string) ([]MediaTranscriptSegment, ModelUsage, string, int64, error)) (string, string, AgentVoiceUsage, error) {
	if len(audio) == 0 || len(audio) > 10<<20 || durationMS <= 0 || durationMS > 60_000 {
		return "", "", AgentVoiceUsage{}, errors.New("invalid voice recording")
	}
	segments, _, language, actualDurationMS, err := call(model)
	var billingFailure *voiceBillingError
	if err != nil && !errors.As(err, &billingFailure) && ctx.Err() == nil && fallback != "" && model != fallback {
		model = fallback
		segments, _, language, actualDurationMS, err = call(model)
	}
	if err != nil {
		return "", "", AgentVoiceUsage{}, err
	}
	parts := make([]string, 0, len(segments))
	for _, segment := range segments {
		if text := strings.TrimSpace(segment.Text); text != "" {
			parts = append(parts, text)
		}
	}
	text := strings.TrimSpace(strings.Join(parts, " "))
	if text == "" {
		return "", "", AgentVoiceUsage{}, errors.New("no speech detected")
	}
	if actualDurationMS <= 0 {
		actualDurationMS = durationMS
	}
	return text, strings.TrimSpace(language), AgentVoiceUsage{Model: model, DurationMS: actualDurationMS}, nil
}

// SpeechProviderError carries only the provider's status, never its response body.
type SpeechProviderError struct{ Status int }

func (e *SpeechProviderError) Error() string {
	return fmt.Sprintf("speech provider status %d", e.Status)
}

func (a *SmartLibraryAnalyzer) TranscribeMedia(ctx context.Context, audio []byte, mimeType string, chunkDurationMS int64) ([]MediaTranscriptSegment, ModelUsage, error) {
	if len(audio) == 0 || len(audio) > 2<<20 || chunkDurationMS <= 0 || chunkDurationMS > 35_000 {
		return nil, ModelUsage{}, errors.New("invalid media audio chunk")
	}
	model := strings.TrimSpace(envconfig.Getenv("MEDIA_SEARCH_TRANSCRIPTION_MODEL"))
	if model == "" {
		model = MediaSearchTranscriptionModel
	}
	configured, primaryModel, fallback, configErr := a.transcriptionModels(ctx, "media-transcription", "media-transcription-fallback", model, envOrDefault("MEDIA_SEARCH_TRANSCRIPTION_FALLBACK_MODEL", MediaSearchTranscriptionFallbackModel))
	if configErr != nil {
		return nil, ModelUsage{}, configErr
	}
	a = configured
	model = primaryModel
	segments, usage, _, _, err := a.transcribeRole(ctx, "media-transcription", audio, mimeType, chunkDurationMS, model)
	if err == nil && len(segments) > 0 {
		return TestingCoalesceTranscriptSegments(segments), usage, nil
	}
	if a.BillingError() != nil || ctx.Err() != nil || fallback == "" || fallback == model {
		return segments, usage, err
	}
	fallbackSegments, fallbackUsage, _, _, fallbackErr := a.transcribeRole(ctx, "media-transcription-fallback", audio, mimeType, chunkDurationMS, fallback)
	usage.InputTokens += fallbackUsage.InputTokens
	usage.OutputTokens += fallbackUsage.OutputTokens
	if fallbackErr != nil {
		return nil, usage, errors.Join(err, fallbackErr)
	}
	return TestingCoalesceTranscriptSegments(fallbackSegments), usage, nil
}

// transcribeWithRoute transcribes one chunk through the agent runtime and
// meters it by audio duration: transcription models report no tokens.
func (a *SmartLibraryAnalyzer) transcribeWithRoute(ctx context.Context, route modelruntime.Route, audio []byte, mimeType string, chunkDurationMS int64, model string) ([]MediaTranscriptSegment, ModelUsage, string, int64, error) {
	attempt, err := a.billing.Begin(ctx, "library.transcription", model, map[string]int64{"audio_ms": chunkDurationMS})
	if err != nil {
		return nil, ModelUsage{}, "", 0, err
	}
	result, callErr := a.Models.Transcribe(ctx, route, model, mimeType, audio)
	actualDurationMS := int64(0)
	if callErr == nil && result.DurationInSeconds != nil {
		seconds := *result.DurationInSeconds
		if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 || seconds > 3600 {
			callErr = errors.New("invalid transcription duration")
		} else {
			actualDurationMS = int64(math.Ceil(seconds * 1000))
		}
	}
	if err := attempt.Finish(ctx, callErr == nil, map[string]int64{"audio_ms": actualDurationMS}, actualDurationMS <= 0); err != nil {
		return nil, ModelUsage{}, "", 0, err
	}
	if callErr != nil {
		var failure *modelruntime.Error
		if errors.As(callErr, &failure) && failure.UpstreamStatus > 0 {
			return nil, ModelUsage{}, "", 0, &SpeechProviderError{Status: failure.UpstreamStatus}
		}
		return nil, ModelUsage{}, "", 0, callErr
	}
	segments := make([]MediaTranscriptSegment, 0, len(result.Segments))
	for _, segment := range result.Segments {
		text := strings.TrimSpace(segment.Text)
		start := max(0, int64(segment.Start*1000))
		end := min(chunkDurationMS, max(start+1, int64(segment.End*1000)))
		if text != "" && start < chunkDurationMS {
			segments = append(segments, MediaTranscriptSegment{StartMS: start, EndMS: end, Text: text})
		}
	}
	if len(segments) == 0 && strings.TrimSpace(result.Text) != "" {
		segments = append(segments, MediaTranscriptSegment{StartMS: 0, EndMS: chunkDurationMS, Text: strings.TrimSpace(result.Text)})
	}
	// Record an audio-duration token estimate so the product cost ledger
	// stays meaningful.
	return segments, ModelUsage{InputTokens: int64(float64(chunkDurationMS) / 1000.0 * 3.0)}, result.Language, actualDurationMS, nil
}

func TestingCoalesceTranscriptSegments(input []MediaTranscriptSegment) []MediaTranscriptSegment {
	if len(input) < 2 {
		return input
	}
	out := make([]MediaTranscriptSegment, 0, len(input)/4+1)
	current := input[0]
	for _, next := range input[1:] {
		gap := next.StartMS - current.EndMS
		duration := current.EndMS - current.StartMS
		endsSentence := strings.HasSuffix(current.Text, ".") || strings.HasSuffix(current.Text, "?") || strings.HasSuffix(current.Text, "!")
		if gap > 1_000 || duration >= 7_000 || (duration >= 2_500 && endsSentence) || len(current.Text)+len(next.Text) > 220 {
			current.Text = strings.TrimSpace(current.Text)
			if current.Text != "" {
				out = append(out, current)
			}
			current = next
			continue
		}
		current.Text = strings.TrimSpace(current.Text + " " + next.Text)
		current.EndMS = max(current.EndMS, next.EndMS)
	}
	current.Text = strings.TrimSpace(current.Text)
	if current.Text != "" {
		out = append(out, current)
	}
	return out
}

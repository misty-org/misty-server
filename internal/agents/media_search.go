package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
)

const (
	MediaSearchTranscriptionModel         = "xai/grok-stt"
	MediaSearchTranscriptionFallbackModel = "openai/whisper-1"
)

type MediaTranscriptSegment struct {
	StartMS int64  `json:"startMs"`
	EndMS   int64  `json:"endMs"`
	Text    string `json:"text"`
}

type mediaTranscriptionResponse struct {
	Text     string `json:"text"`
	Segments []struct {
		Start float64 `json:"startSecond"`
		End   float64 `json:"endSecond"`
		Text  string  `json:"text"`
	} `json:"segments"`
}

func (a *SmartLibraryAnalyzer) TranscribeMedia(ctx context.Context, audio []byte, mimeType string, chunkDurationMS int64) ([]MediaTranscriptSegment, ModelUsage, error) {
	if len(audio) == 0 || len(audio) > 2<<20 || chunkDurationMS <= 0 || chunkDurationMS > 35_000 {
		return nil, ModelUsage{}, errors.New("invalid media audio chunk")
	}
	model := strings.TrimSpace(envconfig.Getenv("MEDIA_SEARCH_TRANSCRIPTION_MODEL"))
	if model == "" {
		model = MediaSearchTranscriptionModel
	}
	segments, usage, err := a.transcribeMediaWithModel(ctx, audio, mimeType, chunkDurationMS, model)
	if err == nil && len(segments) > 0 {
		return TestingCoalesceTranscriptSegments(segments), usage, nil
	}
	fallback := strings.TrimSpace(envconfig.Getenv("MEDIA_SEARCH_TRANSCRIPTION_FALLBACK_MODEL"))
	if fallback == "" {
		fallback = MediaSearchTranscriptionFallbackModel
	}
	if fallback == model {
		return segments, usage, err
	}
	fallbackSegments, fallbackUsage, fallbackErr := a.transcribeMediaWithModel(ctx, audio, mimeType, chunkDurationMS, fallback)
	usage.InputTokens += fallbackUsage.InputTokens
	usage.OutputTokens += fallbackUsage.OutputTokens
	if fallbackErr != nil {
		return nil, usage, errors.Join(err, fallbackErr)
	}
	return TestingCoalesceTranscriptSegments(fallbackSegments), usage, nil
}

func (a *SmartLibraryAnalyzer) transcribeMediaWithModel(ctx context.Context, audio []byte, mimeType string, chunkDurationMS int64, model string) ([]MediaTranscriptSegment, ModelUsage, error) {
	payload, err := json.Marshal(map[string]any{"audio": base64.StdEncoding.EncodeToString(audio), "mediaType": mimeType})
	if err != nil {
		return nil, ModelUsage{}, err
	}

	key := strings.TrimSpace(a.APIKey)
	if key == "" {
		return nil, ModelUsage{}, errors.New("AI Gateway key is required")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(a.embeddingBaseURL(), "/")+"/transcription-model", bytes.NewReader(payload))
	if err != nil {
		return nil, ModelUsage{}, err
	}
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("ai-gateway-protocol-version", "0.0.1")
	request.Header.Set("ai-transcription-model-specification-version", "4")
	request.Header.Set("ai-model-id", model)
	client := a.Client
	if client == nil {
		client = &http.Client{Timeout: 90 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, ModelUsage{}, err
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, ModelUsage{}, fmt.Errorf("AI Gateway transcription status %d: %s", response.StatusCode, strings.TrimSpace(string(raw[:min(len(raw), 256)])))
	}
	var decoded mediaTranscriptionResponse
	if err = json.Unmarshal(raw, &decoded); err != nil {
		return nil, ModelUsage{}, fmt.Errorf("invalid transcription response: %w", err)
	}
	segments := make([]MediaTranscriptSegment, 0, len(decoded.Segments))
	for _, segment := range decoded.Segments {
		text := strings.TrimSpace(segment.Text)
		start := max(0, int64(segment.Start*1000))
		end := min(chunkDurationMS, max(start+1, int64(segment.End*1000)))
		if text != "" && start < chunkDurationMS {
			segments = append(segments, MediaTranscriptSegment{StartMS: start, EndMS: end, Text: text})
		}
	}
	if len(segments) == 0 && strings.TrimSpace(decoded.Text) != "" {
		segments = append(segments, MediaTranscriptSegment{StartMS: 0, EndMS: chunkDurationMS, Text: strings.TrimSpace(decoded.Text)})
	}
	// Gateway's transcription response currently omits token usage. Record an
	// audio-duration estimate so the product cost ledger is still meaningful.
	return segments, ModelUsage{InputTokens: int64(float64(chunkDurationMS) / 1000.0 * 3.0)}, nil
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

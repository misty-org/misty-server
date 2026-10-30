package agent

import (
	"context"
	"errors"

	"github.com/kannachi323/misty/server/internal/aimodels"
	"github.com/kannachi323/misty/server/internal/modelruntime"
)

func (a *SmartLibraryAnalyzer) transcriptionModels(ctx context.Context, primaryRole, fallbackRole, primary, fallback string) (*SmartLibraryAnalyzer, string, string, error) {
	clone := *a
	clone.roleConfigs = map[string]*aimodels.Resolved{}
	for _, role := range []string{primaryRole, fallbackRole} {
		c, err := a.roleConfig(ctx, role)
		if role == fallbackRole && errors.Is(err, aimodels.ErrDisabled) {
			fallback = ""
			continue
		}
		if err != nil {
			return nil, "", "", err
		}
		clone.roleConfigs[role] = c
		if c != nil {
			if role == primaryRole {
				primary = c.Model
			} else {
				fallback = c.Model
			}
		}
	}
	return &clone, primary, fallback, nil
}
func (a *SmartLibraryAnalyzer) transcribeRole(ctx context.Context, role string, audio []byte, mime string, duration int64, model string) ([]MediaTranscriptSegment, ModelUsage, string, int64, error) {
	c, err := a.roleConfig(ctx, role)
	if err != nil {
		return nil, ModelUsage{}, "", 0, err
	}
	if c != nil {
		model = c.Model
	}
	return a.transcribeWithRoute(ctx, modelruntime.For(c), audio, mime, duration, model)
}

// TranscribeAgentVoice transcribes one short recording on the account's
// transcription model, trying the fallback model once when the first fails.
func (a *SmartLibraryAnalyzer) TranscribeAgentVoice(ctx context.Context, audio []byte, mimeType string, durationMS int64, account string) (string, string, AgentVoiceUsage, error) {
	a = a.WithAIAccount(account)
	configured, primary, fallback, err := a.transcriptionModels(ctx, "transcription", "transcription-fallback", envOrDefault("AGENT_TRANSCRIPTION_MODEL", "openai/gpt-4o-mini-transcribe"), MediaSearchTranscriptionFallbackModel)
	if err != nil {
		return "", "", AgentVoiceUsage{}, err
	}
	return transcribeAgentVoiceModels(ctx, audio, durationMS, primary, fallback, func(model string) ([]MediaTranscriptSegment, ModelUsage, string, int64, error) {
		role := "transcription-fallback"
		if model == primary {
			role = "transcription"
		}
		segments, usage, language, actual, err := configured.transcribeRole(ctx, role, audio, mimeType, durationMS, model)
		if err == nil && actual <= 0 {
			actual = durationMS
		}
		return segments, usage, language, actual, err
	})
}

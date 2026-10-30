package api

import (
	"context"
	"log"

	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/byok"
	"github.com/kannachi323/misty/server/internal/modelruntime"
)

// openVoiceTransport opens the account's voice. It uses the voice pipeline
// (ElevenLabs Scribe, the account's fast model, ElevenLabs Flash), several
// times cheaper per spoken reply. Without an ElevenLabs key, or with
// MISTY_VOICE_PIPELINE=realtime, it falls back to native Realtime.
// MISTY_VOICE_ID picks Misty's ElevenLabs voice.
func (s *AgentsService) openVoiceTransport(ctx context.Context, account string) (voiceProvider, error) {
	if envOr("MISTY_VOICE_PIPELINE", "") != "realtime" && s.voiceAnalyzer != nil {
		textRoute, textModel, err := roleRoute(ctx, s.database, account, "routing")
		if err != nil {
			return nil, err
		}
		speechModel := envOr("MISTY_VOICE_TTS_MODEL", "elevenlabs/eleven_flash_v2_5")
		if chosen := accountCredentials(ctx, account).RoleModel("speech"); byok.ProviderOf(chosen) == "elevenlabs" {
			speechModel = chosen
		}
		speechRoute, err := modelruntime.RouteFor(ctx, account, speechModel, "")
		if err != nil {
			return nil, err
		}
		pipeline, err := agent.NewVoicePipeline(ctx, s.voiceAnalyzer.Models, textModel, speechModel, envOr("MISTY_VOICE_ID", ""))
		if err == nil {
			return pipeline.WithRoutes(textRoute, speechRoute), nil
		}
		// Without an ElevenLabs key the account still gets voice, on Realtime.
		log.Printf("voice_pipeline_unavailable: %v", err)
	}
	analyzer, _, err := s.voiceAnalyzer.ConfiguredRealtime(ctx, account)
	if err != nil {
		return nil, err
	}
	return analyzer.OpenVoiceRealtime(ctx)
}

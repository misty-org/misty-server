package api

import (
	"context"
	"log"

	agent "github.com/kannachi323/misty/server/internal/agents"
)

// openVoiceTransport opens the account's voice. Basic accounts use the voice
// pipeline (ElevenLabs Scribe, the fast model, ElevenLabs Flash), several
// times cheaper per spoken reply; paid plans keep native Realtime.
// MISTY_VOICE_ID picks Misty's ElevenLabs voice.
// MISTY_VOICE_PIPELINE=all sends every plan through the pipeline.
func (s *AgentsService) openVoiceTransport(ctx context.Context, account string) (voiceProvider, error) {
	plan := accountPlan(ctx, s.database, account)
	if (plan == "basic" || envOr("MISTY_VOICE_PIPELINE", "") == "all") && s.voiceAnalyzer != nil {
		pipeline, err := agent.NewVoicePipeline(ctx, s.voiceAnalyzer.Models, fastModel(), envOr("MISTY_VOICE_TTS_MODEL", "elevenlabs/eleven_flash_v2_5"), envOr("MISTY_VOICE_ID", ""))
		if err == nil {
			return pipeline, nil
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

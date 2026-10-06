package api

import "context"

// openVoiceTransport opens the realtime voice WebSocket on the account's own
// realtime connection when one is configured, otherwise on the AI Gateway.
func (s *AgentsService) openVoiceTransport(ctx context.Context, account string) (voiceProvider, error) {
	analyzer, _, err := s.voiceAnalyzer.ConfiguredRealtime(ctx, account)
	if err != nil {
		return nil, err
	}
	return analyzer.OpenVoiceRealtime(ctx)
}

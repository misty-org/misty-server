package api

import (
	"context"
	"errors"
	"time"

	"github.com/gorilla/websocket"
)

func (s *AgentsService) openVoiceTransport(ctx context.Context, client *websocket.Conn, rtc bool) (voiceProvider, error) {
	if rtc {
		client.SetReadLimit(100 << 10)
		_ = client.SetReadDeadline(time.Now().Add(20 * time.Second))
		var offer struct {
			Type string `json:"type"`
			SDP  string `json:"sdp"`
		}
		if client.ReadJSON(&offer) != nil || offer.Type != "rtc.offer" {
			return nil, errors.New("invalid voice offer")
		}
		_ = client.SetReadDeadline(time.Time{})
		provider, answer, err := s.voiceAnalyzer.OpenVoiceWebRTC(ctx, offer.SDP)
		if err == nil {
			if err = client.WriteJSON(map[string]string{"type": "rtc.answer", "sdp": answer}); err != nil {
				provider.Close()
				return nil, err
			}
			return provider, nil
		}
		// No input has been accepted or committed. An abandoned call is hung up
		// by the adapter before the gateway transport can receive buffered input.
		if err := client.WriteJSON(map[string]string{"type": "transport.fallback", "transport": "websocket"}); err != nil {
			return nil, err
		}
	}
	return s.voiceAnalyzer.OpenVoiceRealtime(ctx)
}

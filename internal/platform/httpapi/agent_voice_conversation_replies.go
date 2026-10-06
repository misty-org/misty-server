package api

import (
	"encoding/base64"
	"errors"
	"log"
	"strings"
	"time"

	agent "github.com/kannachi323/misty/server/internal/agents"
)

// handleUpstream applies one provider event.
func (s *conversationSession) handleUpstream(e agent.VoiceRealtimeEvent) error {
	switch e.Type {
	case "session-updated":
		if s.ready || !agent.VoiceRealtimeManualSession(e.Raw) || !agent.VoiceRealtimeOutputLimit(e.Raw, agent.VoiceConversationOutputTokens) {
			return errors.New("voice session configuration not confirmed")
		}
		s.ready = true
		s.deadline = time.Now().Add(90 * time.Second)
		return s.write(map[string]string{"type": "ready"})
	case "input-transcription-completed":
		return s.transcriptionCompleted(e)
	case "audio-delta":
		return s.audioDelta(e)
	case "audio-transcript-delta":
		if !s.interrupted {
			s.reply += e.Delta
			if len(s.reply) > 32000 {
				return errors.New("voice transcript limit")
			}
		}
		return nil
	case "function-call-arguments-done":
		return s.functionCall(e)
	case "response-done":
		return s.responseCompleted(e)
	case "error":
		return errors.New("voice provider rejected session: " + e.Code)
	case "input-transcription-failed":
		return errors.New("voice transcription failed")
	case "speech-started":
		return errors.New("unexpected voice activity detection")
	}
	return nil
}

func (s *conversationSession) transcriptionCompleted(e agent.VoiceRealtimeEvent) error {
	if !s.busy || s.transcribed || !s.input.Committed {
		return errors.New("unexpected voice transcription")
	}
	u, err := agent.RealtimeTranscriptionUsage(e.Raw)
	if err != nil {
		return err
	}
	s.usage.Add(u)
	s.transcribed = true
	s.prompt = voiceBoundText(e.Transcript, 16000)
	if err := s.h.Save(s.ctx, s.turnID, s.prompt, "", false, s.started); err != nil {
		return err
	}
	if err := s.write(map[string]string{"type": "transcript", "text": s.prompt, "id": s.turnID}); err != nil {
		return err
	}
	if err := s.finish(); err != nil {
		return err
	}
	return s.dispatchTool()
}

func (s *conversationSession) audioDelta(e agent.VoiceRealtimeEvent) error {
	if !s.responding {
		return errors.New("unsolicited voice output")
	}
	if s.interrupted {
		return nil
	}
	b, err := base64.StdEncoding.DecodeString(e.Delta)
	if err != nil {
		return err
	}
	s.outputBytes += len(b)
	if s.outputBytes > 24000*2*120 {
		return errors.New("voice output limit")
	}
	if s.outputBytes == len(b) {
		log.Printf("voice_conversation_first_audio operation_id=%q turn=%d latency_ms=%d", s.h.Operation, s.turns, time.Since(s.committed).Milliseconds())
	}
	s.currentItem = e.ItemID
	s.deadline = time.Now().Add(45 * time.Second)
	return s.write(map[string]string{"type": "audio", "audio": e.Delta, "itemId": e.ItemID})
}

func (s *conversationSession) functionCall(e agent.VoiceRealtimeEvent) error {
	if s.resultOnly {
		return errors.New("task result speech cannot invoke tools")
	}
	if s.interrupted {
		return nil
	}
	if !s.responding || s.pending != nil || s.seen[e.CallID] || s.toolCount >= 4 {
		return errors.New("voice tool limit or replay")
	}
	t, err := parseConversationTool(e)
	if err != nil {
		return err
	}
	s.seen[t.ID] = true
	s.toolCount++
	t.Key = s.turnID + ":" + t.ID
	s.pending = &t
	return nil
}

func (s *conversationSession) responseCompleted(e agent.VoiceRealtimeEvent) error {
	if !s.responding {
		return errors.New("unexpected voice completion")
	}
	u, err := agent.RealtimeResponseUsage(e.Raw)
	if err != nil {
		return err
	}
	s.usage.Add(u)
	s.responding = false
	if s.interrupted {
		if err := s.cancelPending(); err != nil {
			return err
		}
		s.responseDone = true
		return s.finish()
	}
	if e.Status != "completed" {
		return errors.New("voice response did not complete")
	}
	if s.pending != nil {
		return s.dispatchTool()
	}
	s.responseDone = true
	s.deadline = time.Now().Add(130 * time.Second)
	if err := s.write(map[string]string{"type": "audio.done"}); err != nil {
		return err
	}
	return s.finish()
}

// generate admits the full upper bound of one more spoken response. Audio
// context is retained by the provider across turns, including earlier replies
// and uncached input.
func (s *conversationSession) generate() error {
	if s.contextBytes > 60000 {
		return errors.New("voice context limit reached")
	}
	if err := s.h.Access(s.ctx); err != nil {
		return err
	}
	estimate := agent.RealtimeVoiceUsage{"context_bytes": int64(s.contextBytes), "audio_pcm_bytes": int64(s.totalInput), "retained_audio_tokens": s.usage["output_audio_tokens"], "output_token_limit": agent.VoiceConversationOutputTokens}
	if err := s.h.Reserve(s.ctx, estimate); err != nil {
		return err
	}
	if s.reply != "" {
		s.reply = strings.TrimRight(s.reply, " \t\r\n") + " "
	}
	s.responding = true
	s.responseDone = false
	s.deadline = time.Now().Add(45 * time.Second)
	return s.provider.Send(map[string]string{"type": "response-create"})
}

func (s *conversationSession) toolOutput(t conversationTool, result string) error {
	result = voiceBoundText(result, 24000)
	s.contextBytes += len(result) + len(t.Instruction) + 256
	if err := s.provider.Send(map[string]any{"type": "conversation-item-create", "item": map[string]string{"type": "function-call-output", "callId": t.ID, "name": t.Name, "output": result}}); err != nil {
		return err
	}
	return s.generate()
}

func (s *conversationSession) dispatchTool() error {
	if s.pending == nil || s.responding || !s.transcribed || s.interrupted {
		return nil
	}
	if err := s.h.Access(s.ctx); err != nil {
		return err
	}
	// Both provider response and input transcription are now measured. Close
	// those holds before admitting business work or another voice response.
	// Otherwise a tool turn unnecessarily competes with its own unused hold.
	if err := s.h.Settle(s.ctx, s.usage); err != nil {
		return err
	}
	if s.pending.Name == "get_context" || s.pending.Name == "get_task_status" {
		result, err := s.h.Tool(s.ctx, s.pending.Name)
		if err != nil {
			return err
		}
		t := *s.pending
		s.pending = nil
		return s.toolOutput(t, result)
	}
	s.deadline = time.Now().Add(70 * time.Second)
	if _, err := s.h.Tool(s.ctx, "get_task_status"); err != nil {
		return err
	}
	return s.write(map[string]any{"type": "tool.call", "callId": s.pending.ID, "name": s.pending.Name, "instruction": s.pending.Instruction, "key": s.pending.Key, "invocationId": s.h.TaskID()})
}

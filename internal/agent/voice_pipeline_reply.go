package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/modelruntime"
)

// voiceTurn is the text model's answer: what to say, or one tool to call.
type voiceTurn struct {
	Say         string `json:"say"`
	Tool        string `json:"tool"`
	Instruction string `json:"instruction"`
}

func (p *VoicePipeline) reply(ctx context.Context) {
	// Wait for the turn being transcribed: the reply must see what was said.
	p.mu.Lock()
	heard := p.heard
	p.mu.Unlock()
	if heard != nil {
		select {
		case <-heard:
		case <-ctx.Done():
			// Interrupted before speaking: report the response as cancelled so
			// the session can finish the turn.
			raw, _ := json.Marshal(map[string]any{"misty_pipeline_usage": map[string]int64{}, "response": map[string]any{"status": "cancelled"}})
			p.emit(VoiceRealtimeEvent{Type: "response-done", ResponseID: "resp_" + uuid.NewString(), Status: "cancelled", Raw: raw})
			return
		}
	}
	p.mu.Lock()
	if p.heardFail {
		// The session has already failed this turn on the transcription error.
		p.mu.Unlock()
		return
	}
	history := append([]modelruntime.Message(nil), p.history...)
	tools := append([]string{""}, p.toolNames...)
	if last := lastText(p.history); strings.HasPrefix(last, VoiceTaskResultPrompt) {
		// Reading a finished task's result aloud: speak only, never act.
		tools = []string{""}
	}
	system := p.instructions + "\n\nAnswer as JSON. Put what to say aloud in say. To use a tool, set tool to its name and instruction to its argument (empty for tools without one) and leave say empty. Spoken answers are plain sentences: no markdown, lists or links."
	limit := min(p.maxOutput, pipelineReplyTokens)
	p.mu.Unlock()
	if len(history) == 0 {
		history = []modelruntime.Message{{Role: "user", Content: []modelruntime.Part{modelruntime.Text("(The user has not spoken yet.)")}}}
	}
	responseID, itemID := "resp_"+uuid.NewString(), "item_"+uuid.NewString()
	usage := map[string]int64{"input_tokens": 0, "output_tokens": 0, "tts_characters": 0}
	done := func(status string) {
		raw, _ := json.Marshal(map[string]any{"misty_pipeline_usage": usage, "response": map[string]any{"status": status}})
		p.emit(VoiceRealtimeEvent{Type: "response-done", ResponseID: responseID, Status: status, Raw: raw})
	}
	result, err := p.models.Text(ctx, modelruntime.TextRequest{
		Route: p.textRoute, Model: p.textModel, System: system, Messages: history, MaxOutputTokens: int64(limit),
		Schema: &modelruntime.Schema{Name: "voice_turn", Schema: map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"say", "tool", "instruction"},
			"properties": map[string]any{"say": map[string]string{"type": "string"}, "tool": map[string]any{"type": "string", "enum": tools}, "instruction": map[string]string{"type": "string"}},
		}},
	})
	usage["input_tokens"], usage["output_tokens"] = result.Usage.InputTokens, min(result.Usage.OutputTokens, int64(limit))
	if ctx.Err() != nil {
		done("cancelled")
		return
	}
	var turn voiceTurn
	if err != nil || json.Unmarshal(result.Object, &turn) != nil {
		if err == nil && strings.TrimSpace(result.Text) != "" {
			turn.Say = result.Text
		} else {
			p.emit(VoiceRealtimeEvent{Type: "error", Code: "voice_reply_failed"})
			return
		}
	}
	if turn.Tool != "" && p.tools[turn.Tool] {
		args := map[string]string{}
		if turn.Instruction != "" {
			args["instruction"] = turn.Instruction
		}
		encoded, _ := json.Marshal(args)
		p.mu.Lock()
		p.remember("assistant", "(Called "+turn.Tool+".)")
		p.mu.Unlock()
		p.emit(VoiceRealtimeEvent{Type: "function-call-arguments-done", ResponseID: responseID, ItemID: itemID, CallID: "call_" + uuid.NewString()[:12], Name: turn.Tool, Arguments: string(encoded)})
		done("completed")
		return
	}
	say := strings.TrimSpace(turn.Say)
	if runes := []rune(say); len(runes) > pipelineSpeechChars {
		say = string(runes[:pipelineSpeechChars-40]) + ". The rest is in the conversation."
	}
	if say == "" {
		done("completed")
		return
	}
	p.mu.Lock()
	p.remember("assistant", say)
	p.mu.Unlock()
	p.emit(VoiceRealtimeEvent{Type: "audio-transcript-delta", ResponseID: responseID, ItemID: itemID, Delta: say})
	// Speak sentence by sentence so the first sound plays after one short
	// synthesis, not the whole reply; the next sentence is prepared while the
	// current one plays. Each request is charged by its characters, even if
	// the reply is then cancelled.
	type spoken struct {
		pcm []byte
		err error
	}
	sentences := splitSentences(say)
	speak := func(i int) chan spoken {
		out := make(chan spoken, 1)
		usage["tts_characters"] += int64(utf8.RuneCountInString(sentences[i]))
		request := modelruntime.SpeechRequest{Route: p.speechRoute, Model: p.ttsModel, Text: sentences[i], Voice: p.voice}
		if i > 0 {
			request.PreviousText = strings.Join(sentences[:i], " ")
		}
		if i+1 < len(sentences) {
			request.NextText = sentences[i+1]
		}
		go func() {
			pcm, err := p.models.Speak(ctx, request)
			out <- spoken{pcm, err}
		}()
		return out
	}
	next := speak(0)
	for i := range sentences {
		current := next
		if i+1 < len(sentences) {
			next = speak(i + 1)
		}
		result := <-current
		if ctx.Err() != nil {
			done("cancelled")
			return
		}
		if result.err != nil {
			p.emit(VoiceRealtimeEvent{Type: "error", Code: "voice_speech_failed"})
			return
		}
		// Quarter-second chunks keep playback responsive and within frame limits.
		for start := 0; start < len(result.pcm); start += 12000 {
			end := min(start+12000, len(result.pcm))
			p.emit(VoiceRealtimeEvent{Type: "audio-delta", ResponseID: responseID, ItemID: itemID, Delta: base64.StdEncoding.EncodeToString(result.pcm[start:end])})
		}
	}
	done("completed")
}

// lastText is the text of the conversation's latest message.
func lastText(history []modelruntime.Message) string {
	if len(history) == 0 || len(history[len(history)-1].Content) == 0 {
		return ""
	}
	return history[len(history)-1].Content[0].Text
}

// splitSentences breaks a spoken reply at sentence ends (including Chinese and
// Japanese punctuation) into pieces worth a separate synthesis: a fragment
// shorter than a few words joins its neighbour.
func splitSentences(text string) []string {
	var pieces []string
	var current strings.Builder
	runes := []rune(text)
	for i, r := range runes {
		current.WriteRune(r)
		end := strings.ContainsRune(".!?。！？", r)
		if r == '.' || r == '!' || r == '?' {
			// Latin punctuation ends a sentence only before a space or the end.
			end = i+1 == len(runes) || runes[i+1] == ' ' || runes[i+1] == '\n'
		}
		if end && speechWeight(current.String()) >= 12 {
			pieces = append(pieces, strings.TrimSpace(current.String()))
			current.Reset()
		}
	}
	if rest := strings.TrimSpace(current.String()); rest != "" {
		if len(pieces) > 0 && speechWeight(rest) < 12 {
			pieces[len(pieces)-1] = joinSpoken(pieces[len(pieces)-1], rest)
		} else {
			pieces = append(pieces, rest)
		}
	}
	return pieces
}

// speechWeight approximates spoken length: a CJK character carries about as
// much as three Latin letters.
func speechWeight(text string) int {
	weight := 0
	for _, r := range strings.TrimSpace(text) {
		if unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul) {
			weight += 3
		} else {
			weight++
		}
	}
	return weight
}

// joinSpoken joins two pieces, with a space only between Latin text.
func joinSpoken(a, b string) string {
	last, _ := utf8.DecodeLastRuneInString(a)
	if unicode.In(last, unicode.Han, unicode.Hiragana, unicode.Katakana) || strings.ContainsRune("。！？", last) {
		return a + b
	}
	return a + " " + b
}

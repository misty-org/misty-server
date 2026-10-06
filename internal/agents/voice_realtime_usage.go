package agent

import (
	"encoding/json"
	"errors"
)

// Usage is provider-measured, with no prices at this boundary. Cached counts
// are subsets of input counts. Missing/ambiguous usage must retain its hold.
type RealtimeVoiceUsage map[string]int64

func (u RealtimeVoiceUsage) Add(other RealtimeVoiceUsage) {
	for key, value := range other {
		u[key] += value
	}
}

func RealtimeResponseUsage(raw json.RawMessage) (RealtimeVoiceUsage, error) {
	var event struct {
		Response struct {
			Usage *struct {
				Input        *int64 `json:"input_tokens"`
				Output       *int64 `json:"output_tokens"`
				InputDetails *struct {
					Text   *int64 `json:"text_tokens"`
					Audio  *int64 `json:"audio_tokens"`
					Cached *int64 `json:"cached_tokens"`
					Cache  struct {
						Text  int64 `json:"text_tokens"`
						Audio int64 `json:"audio_tokens"`
					} `json:"cached_tokens_details"`
				} `json:"input_token_details"`
				OutputDetails *struct {
					Text  *int64 `json:"text_tokens"`
					Audio *int64 `json:"audio_tokens"`
				} `json:"output_token_details"`
			} `json:"usage"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &event) != nil || event.Response.Usage == nil {
		return nil, errors.New("realtime usage missing")
	}
	u := event.Response.Usage
	if u.Input == nil || u.Output == nil || u.InputDetails == nil || u.OutputDetails == nil {
		return nil, errors.New("realtime usage details missing")
	}
	i, o := u.InputDetails, u.OutputDetails
	if i.Text == nil || i.Audio == nil || i.Cached == nil || o.Text == nil || o.Audio == nil {
		return nil, errors.New("realtime usage counters missing")
	}
	units := RealtimeVoiceUsage{"input_text_tokens": *i.Text, "input_audio_tokens": *i.Audio,
		"cached_input_text_tokens": i.Cache.Text, "cached_input_audio_tokens": i.Cache.Audio,
		"output_text_tokens": *o.Text, "output_audio_tokens": *o.Audio}
	for _, value := range units {
		if value < 0 || value > 1_000_000 {
			return nil, errors.New("invalid realtime usage")
		}
	}
	if *i.Text+*i.Audio != *u.Input || *o.Text+*o.Audio != *u.Output || i.Cache.Text+i.Cache.Audio != *i.Cached || i.Cache.Text > *i.Text || i.Cache.Audio > *i.Audio {
		return nil, errors.New("inconsistent realtime usage")
	}
	return units, nil
}

func RealtimeTranscriptionUsage(raw json.RawMessage) (RealtimeVoiceUsage, error) {
	var event struct {
		Usage *struct {
			Type   string `json:"type"`
			Input  *int64 `json:"input_tokens"`
			Output *int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(raw, &event) != nil || event.Usage == nil || event.Usage.Type != "tokens" || event.Usage.Input == nil || event.Usage.Output == nil || *event.Usage.Input < 0 || *event.Usage.Output < 0 || *event.Usage.Input > 100000 || *event.Usage.Output > 100000 {
		return nil, errors.New("transcription usage missing or invalid")
	}
	return RealtimeVoiceUsage{"transcription_input_tokens": *event.Usage.Input, "transcription_output_tokens": *event.Usage.Output}, nil
}

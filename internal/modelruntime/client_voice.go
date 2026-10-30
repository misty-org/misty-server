package modelruntime

import (
	"context"
	"encoding/base64"
)

type TranscribeRequest struct {
	Route     Route  `json:"route"`
	Model     string `json:"model"`
	MediaType string `json:"mediaType"`
	Audio     string `json:"audio"`
}

type Segment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

type TranscribeResult struct {
	Text              string    `json:"text"`
	Language          string    `json:"language"`
	DurationInSeconds *float64  `json:"durationInSeconds"`
	Segments          []Segment `json:"segments"`
}

func (c *Client) Transcribe(ctx context.Context, route Route, model, mediaType string, audio []byte) (TranscribeResult, error) {
	var result TranscribeResult
	err := c.post(ctx, "/v1/models/transcribe", TranscribeRequest{
		Route: route, Model: model, MediaType: mediaType, Audio: base64.StdEncoding.EncodeToString(audio),
	}, &result)
	return result, err
}

// DiarizedRequest transcribes a file with speaker labels. Context carries the
// person's terms and what the audio is about, which improves names.
type DiarizedRequest struct {
	Route     Route            `json:"route"`
	Model     string           `json:"model"`
	MediaType string           `json:"mediaType"`
	Audio     string           `json:"audio"`
	Languages []string         `json:"languages,omitempty"`
	Context   *DiarizedContext `json:"context,omitempty"`
}

type DiarizedContext struct {
	Text  string   `json:"text,omitempty"`
	Terms []string `json:"terms,omitempty"`
}

// Turn is one speaker's continuous speech, in seconds from the start.
type Turn struct {
	Speaker  string  `json:"speaker"`
	Language string  `json:"language,omitempty"`
	Start    float64 `json:"start"`
	End      float64 `json:"end"`
	Text     string  `json:"text"`
}

type DiarizedResult struct {
	Text              string   `json:"text"`
	Language          string   `json:"language"`
	DurationInSeconds *float64 `json:"durationInSeconds"`
	Turns             []Turn   `json:"turns"`
}

func (c *Client) TranscribeDiarized(ctx context.Context, route Route, model, mediaType string, audio []byte, languages []string, context *DiarizedContext) (DiarizedResult, error) {
	var result DiarizedResult
	err := c.post(ctx, "/v1/models/transcribe-diarized", DiarizedRequest{
		Route: route, Model: model, MediaType: mediaType, Audio: base64.StdEncoding.EncodeToString(audio), Languages: languages, Context: context,
	}, &result)
	return result, err
}

// VoiceNotesRequest turns a transcript, and the person's own notes, into
// notes, a summary, action items or chapters.
type VoiceNotesRequest struct {
	Route           Route  `json:"route"`
	Model           string `json:"model"`
	Mode            string `json:"mode"`
	Title           string `json:"title,omitempty"`
	Turns           []Turn `json:"turns"`
	UserNotes       string `json:"userNotes,omitempty"`
	Template        string `json:"template,omitempty"`
	Language        string `json:"language,omitempty"`
	MaxOutputTokens int64  `json:"maxOutputTokens,omitempty"`
}

type ActionItem struct {
	Text  string `json:"text"`
	Owner string `json:"owner,omitempty"`
	Due   string `json:"due,omitempty"`
}

type Chapter struct {
	Start   float64 `json:"start"`
	Title   string  `json:"title"`
	Summary string  `json:"summary"`
}

type VoiceNotesResult struct {
	Title       string       `json:"title"`
	Markdown    string       `json:"markdown"`
	ActionItems []ActionItem `json:"actionItems"`
	Chapters    []Chapter    `json:"chapters"`
	Usage       Usage        `json:"usage"`
}

func (c *Client) VoiceNotes(ctx context.Context, request VoiceNotesRequest) (VoiceNotesResult, error) {
	var result VoiceNotesResult
	err := c.post(ctx, "/v1/models/voice-notes", request, &result)
	return result, err
}

// DaySummaryRequest summarizes one day of the person's Memory.
type DaySummaryRequest struct {
	Route           Route     `json:"route"`
	Model           string    `json:"model"`
	Date            string    `json:"date"`
	Items           []DayItem `json:"items"`
	Language        string    `json:"language,omitempty"`
	MaxOutputTokens int64     `json:"maxOutputTokens,omitempty"`
}

type DayItem struct {
	At      string `json:"at"`
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	Site    string `json:"site,omitempty"`
	Excerpt string `json:"excerpt,omitempty"`
}

type DaySummaryResult struct {
	Markdown string   `json:"markdown"`
	Topics   []string `json:"topics"`
	Usage    Usage    `json:"usage"`
}

func (c *Client) DaySummary(ctx context.Context, request DaySummaryRequest) (DaySummaryResult, error) {
	var result DaySummaryResult
	err := c.post(ctx, "/v1/models/day-summary", request, &result)
	return result, err
}

// SpeechRequest speaks text for the voice pipeline. The runtime returns raw
// 24 kHz 16-bit mono PCM.
type SpeechRequest struct {
	Route Route  `json:"route"`
	Model string `json:"model"`
	Text  string `json:"text"`
	Voice string `json:"voice,omitempty"`
	// The neighbouring sentences of a reply spoken in pieces, so the delivery
	// stays continuous across the joins.
	PreviousText string `json:"previousText,omitempty"`
	NextText     string `json:"nextText,omitempty"`
}

func (c *Client) Speak(ctx context.Context, request SpeechRequest) ([]byte, error) {
	var result struct {
		Audio string `json:"audio"`
	}
	if err := c.post(ctx, "/v1/models/speech", request, &result); err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(result.Audio)
}

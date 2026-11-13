package api

import "encoding/json"

// What the desktop sends back after transcribing a file or writing a Note on
// its own models, and what it writes the Note from.

type voiceTurnResult struct {
	Speaker  string  `json:"speaker"`
	Language string  `json:"language,omitempty"`
	Start    float64 `json:"start"`
	End      float64 `json:"end"`
	Text     string  `json:"text"`
}

type diarizedResult struct {
	Text              string            `json:"text"`
	Language          string            `json:"language"`
	DurationInSeconds *float64          `json:"durationInSeconds"`
	Turns             []voiceTurnResult `json:"turns"`
}

type voiceActionItem struct {
	Text  string `json:"text"`
	Owner string `json:"owner,omitempty"`
	Due   string `json:"due,omitempty"`
}

type voiceChapter struct {
	Start   float64 `json:"start"`
	Title   string  `json:"title"`
	Summary string  `json:"summary"`
}

type voiceNotesResult struct {
	Title       string            `json:"title"`
	Markdown    string            `json:"markdown"`
	ActionItems []voiceActionItem `json:"actionItems"`
	Chapters    []voiceChapter    `json:"chapters"`
	Usage       json.RawMessage   `json:"usage,omitempty"`
}

type voiceNotesRequest struct {
	Mode            string            `json:"mode"`
	Title           string            `json:"title,omitempty"`
	Turns           []voiceTurnResult `json:"turns"`
	UserNotes       string            `json:"userNotes,omitempty"`
	Template        string            `json:"template,omitempty"`
	Language        string            `json:"language,omitempty"`
	MaxOutputTokens int64             `json:"maxOutputTokens,omitempty"`
}

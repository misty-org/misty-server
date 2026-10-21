package api

import (
	"strings"
	"testing"
)

func TestVideoContextIsBoundedAndOnlyForTheCursor(t *testing.T) {
	media := &aiMediaContext{Title: "Gradient descent", URL: "https://www.youtube.com/watch?v=x", Time: 754, Paused: true}
	for index := 0; index < 400; index++ {
		media.Transcript = append(media.Transcript, aiMediaSaidLine{T: float64(index), Text: strings.Repeat("word ", 20)})
	}
	body := aiInvocationInput{Mode: "drawer", MediaContext: media}
	if validateMediaContext(&body) == nil {
		t.Fatal("video context accepted outside the cursor")
	}
	body.Mode = "companion"
	if err := validateMediaContext(&body); err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, line := range media.Transcript {
		total += len(line.Text) + 1
	}
	if len(media.Transcript) == 0 || total > maxMediaTranscriptChars || media.Transcript[len(media.Transcript)-1].T >= 400 {
		t.Fatalf("transcript not bounded: %d lines, %d chars", len(media.Transcript), total)
	}
	body.MediaContext = &aiMediaContext{Time: -1}
	if validateMediaContext(&body) == nil {
		t.Fatal("negative time accepted")
	}
	body.MediaContext = &aiMediaContext{Time: 3, URL: "javascript:alert(1)"}
	if err := validateMediaContext(&body); err != nil || body.MediaContext.URL != "" {
		t.Fatalf("unsafe address kept: %q, %v", body.MediaContext.URL, err)
	}
}

func TestVideoContextReadsAsPageData(t *testing.T) {
	duration := 3725.0
	text := mediaContextPrompt(&aiMediaContext{
		Title: "Gradient descent", URL: "https://www.youtube.com/watch?v=x", Time: 754, Duration: &duration, Paused: true,
		Transcript: []aiMediaSaidLine{{T: 750, Text: "so we step against the gradient"}},
	})
	for _, want := range []string{"untrusted page data", "paused at 12:34 of 1:02:05", "[12:30] so we step against the gradient"} {
		if !strings.Contains(text, want) {
			t.Fatalf("video context lacks %q:\n%s", want, text)
		}
	}
	if !strings.Contains(mediaContextPrompt(&aiMediaContext{Time: 5}), "captions are not available") {
		t.Fatal("missing captions are not explained")
	}
	if mediaContextPrompt(nil) != "" {
		t.Fatal("a turn without a video gained video context")
	}
}

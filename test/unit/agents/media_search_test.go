package agent

import (
	"context"
	"net/http"
	"testing"

	. "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/modelruntime"
)

func TestTranscribeMediaReturnsTimestampedSegments(t *testing.T) {
	runtime := modelruntime.TestingNewFake(t, func(call modelruntime.TestingCall) (int, any) {
		if call.Path != "/v1/models/transcribe" || call.Body["model"] != MediaSearchTranscriptionModel || call.Body["mediaType"] != "audio/mpeg" || call.Body["audio"] == "" {
			t.Fatalf("call=%s %v", call.Path, call.Body)
		}
		if call.Body["route"].(map[string]any)["provider"] != "instance" {
			t.Fatalf("route=%v", call.Body["route"])
		}
		return http.StatusOK, map[string]any{"text": "hello world", "segments": []map[string]any{{"start": 1.25, "end": 2.5, "text": "hello world"}}}
	})
	analyzer := &SmartLibraryAnalyzer{Models: runtime.Client}
	segments, _, err := analyzer.TranscribeMedia(context.Background(), []byte("fake mp3"), "audio/mpeg", 30_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 1 || segments[0].StartMS != 1250 || segments[0].EndMS != 2500 || segments[0].Text != "hello world" {
		t.Fatalf("segments=%+v", segments)
	}
}

func TestCoalesceTranscriptSegmentsPreservesUsefulTimestamps(t *testing.T) {
	input := []MediaTranscriptSegment{{StartMS: 100, EndMS: 400, Text: "hello"}, {StartMS: 410, EndMS: 900, Text: "there"}, {StartMS: 920, EndMS: 3_100, Text: "friend."}, {StartMS: 3_150, EndMS: 3_600, Text: "next"}}
	got := TestingCoalesceTranscriptSegments(input)
	if len(got) != 2 || got[0].StartMS != 100 || got[0].EndMS != 3100 || got[0].Text != "hello there friend." || got[1].StartMS != 3150 {
		t.Fatalf("got=%+v", got)
	}
}
func TestTranscribeMediaRejectsOversizedAndLongChunks(t *testing.T) {
	analyzer := &SmartLibraryAnalyzer{}
	if _, _, err := analyzer.TranscribeMedia(context.Background(), make([]byte, (2<<20)+1), "audio/mpeg", 30_000); err == nil {
		t.Fatal("oversized audio accepted")
	}
	if _, _, err := analyzer.TranscribeMedia(context.Background(), []byte("x"), "audio/mpeg", 35_001); err == nil {
		t.Fatal("long chunk accepted")
	}
}

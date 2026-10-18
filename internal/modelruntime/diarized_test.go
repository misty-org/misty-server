package modelruntime

import (
	"net/http"
	"testing"
)

func TestDiarizedTranscriptionSendsContextAndReadsTurns(t *testing.T) {
	fake := TestingNewFake(t, func(call TestingCall) (int, any) {
		return http.StatusOK, map[string]any{"text": "Hi. Hello.", "language": "en", "turns": []map[string]any{
			{"speaker": "1", "start": 0, "end": 1.2, "text": "Hi."},
			{"speaker": "2", "start": 1.4, "end": 2.0, "text": "Hello."},
		}}
	})
	result, err := fake.Client.TranscribeDiarized(t.Context(), "soniox/stt-async-v4", "audio/wav", []byte("RIFF"), []string{"en"}, &DiarizedContext{Terms: []string{"Misty"}})
	if err != nil || len(result.Turns) != 2 || result.Turns[1].Speaker != "2" || result.Turns[1].Start != 1.4 {
		t.Fatalf("TranscribeDiarized() = %+v, %v", result, err)
	}
	call := fake.Calls()[0]
	if call.Path != "/v1/models/transcribe-diarized" || call.Body["model"] != "soniox/stt-async-v4" {
		t.Fatalf("call = %+v", call)
	}
	context, _ := call.Body["context"].(map[string]any)
	if terms, _ := context["terms"].([]any); len(terms) != 1 || terms[0] != "Misty" {
		t.Fatalf("context = %+v", call.Body["context"])
	}
	if route, _ := call.Body["route"].(map[string]any); route["provider"] != "instance" {
		t.Fatalf("route = %+v", call.Body["route"])
	}
}

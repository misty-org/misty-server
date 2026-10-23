package db

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestVoiceTranscriptsStayWithTheirOwnerAndKeepSpeakerNames(t *testing.T) {
	database := voiceTestDB(t)
	ctx := context.Background()
	transcript := VoiceTranscript{
		ID: uuid.NewString(), Title: "Standup", Source: VoiceSource{Kind: "tab", URL: "https://meet.example/abc", Title: "Meet"},
		Language: "en", DurationMS: 61_000,
		Turns: []VoiceTurn{{Speaker: "Speaker 1", Channel: "system", Start: 0, End: 2.5, Text: "Morning."}, {Speaker: "Speaker 2", Start: 3, End: 5, Text: "Hi."}},
	}
	if err := database.SaveVoiceTranscript(ctx, "owner", transcript); err != nil {
		t.Fatal(err)
	}
	// Invalid transcripts are refused before they reach the database.
	bad := transcript
	bad.ID, bad.Source.Kind = uuid.NewString(), "camera"
	if err := database.SaveVoiceTranscript(ctx, "owner", bad); !errors.Is(err, ErrSpaceInvalid) {
		t.Fatalf("unknown source kind = %v", err)
	}
	if _, err := database.VoiceTranscript(ctx, "other", transcript.ID); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("another person read the transcript: %v", err)
	}
	if err := database.UpdateVoiceTranscript(ctx, "other", transcript.ID, map[string]string{"Speaker 1": "Mallory"}, "", ""); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("another person renamed a speaker: %v", err)
	}
	if err := database.UpdateVoiceTranscript(ctx, "owner", transcript.ID, map[string]string{"Speaker 1": "Ana"}, "", ""); err != nil {
		t.Fatal(err)
	}
	// Linking a Note leaves the speaker names alone.
	if err := database.UpdateVoiceTranscript(ctx, "owner", transcript.ID, nil, "note-1", ""); err != nil {
		t.Fatal(err)
	}
	if err := database.UpdateVoiceTranscript(ctx, "owner", transcript.ID, map[string]string{"Speaker 2": " "}, "", ""); !errors.Is(err, ErrSpaceInvalid) {
		t.Fatalf("blank speaker name = %v", err)
	}
	got, err := database.VoiceTranscript(ctx, "owner", transcript.ID)
	if err != nil || got.Speakers["Speaker 1"] != "Ana" || got.NoteID != "note-1" || len(got.Turns) != 2 || got.Turns[0].Channel != "system" || got.Source.URL != transcript.Source.URL {
		t.Fatalf("read back = %+v, %v", got, err)
	}
	if err := database.DeleteVoiceTranscript(ctx, "other", transcript.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.VoiceTranscript(ctx, "owner", transcript.ID); err != nil {
		t.Fatalf("another person deleted the transcript: %v", err)
	}
	if err := database.DeleteVoiceTranscript(ctx, "owner", transcript.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.VoiceTranscript(ctx, "owner", transcript.ID); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatalf("deleted transcript still reads: %v", err)
	}
}

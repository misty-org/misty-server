package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// VoiceTurn is one speaker's continuous speech, in seconds from the start.
type VoiceTurn struct {
	Speaker  string  `json:"speaker"`
	Channel  string  `json:"channel,omitempty"`
	Language string  `json:"language,omitempty"`
	Start    float64 `json:"start"`
	End      float64 `json:"end"`
	Text     string  `json:"text"`
}

// VoiceSource says where the speech came from.
type VoiceSource struct {
	Kind          string `json:"kind"`
	URL           string `json:"url,omitempty"`
	Title         string `json:"title,omitempty"`
	LibraryItemID string `json:"library_item_id,omitempty"`
}

type VoiceTranscript struct {
	ID              string            `json:"id"`
	Title           string            `json:"title"`
	Source          VoiceSource       `json:"source"`
	Language        string            `json:"language"`
	Turns           []VoiceTurn       `json:"turns"`
	Speakers        map[string]string `json:"speakers"`
	DurationMS      int64             `json:"duration_ms"`
	NoteID          string            `json:"note_id"`
	RecordingItemID string            `json:"recording_item_id"`
	CreatedAt       time.Time         `json:"created_at"`
}

const (
	maxVoiceTurns     = 20_000
	maxVoiceTurnBytes = 20 << 10
	maxVoiceSpeakers  = 64
)

var voiceSourceKinds = map[string]bool{"mic": true, "tab": true, "system": true, "app": true, "file": true, "library_item": true, "recording": true}

func validVoiceTranscript(t VoiceTranscript) bool {
	if t.ID == "" || len(t.Title) > 200 || len(t.Language) > 16 || !voiceSourceKinds[t.Source.Kind] || len(t.Turns) == 0 || len(t.Turns) > maxVoiceTurns || len(t.Speakers) > maxVoiceSpeakers || t.DurationMS < 0 {
		return false
	}
	for _, turn := range t.Turns {
		if turn.Speaker == "" || len(turn.Speaker) > 80 || len(turn.Text) > maxVoiceTurnBytes || turn.End < turn.Start || turn.Start < 0 {
			return false
		}
	}
	return validSpeakerNames(t.Speakers)
}

func validSpeakerNames(names map[string]string) bool {
	if len(names) > maxVoiceSpeakers {
		return false
	}
	for key, name := range names {
		if key == "" || len(key) > 80 || len(strings.TrimSpace(name)) == 0 || len(name) > 80 {
			return false
		}
	}
	return true
}

func (db *Database) SaveVoiceTranscript(ctx context.Context, user string, t VoiceTranscript) error {
	if user == "" || !validVoiceTranscript(t) {
		return ErrSpaceInvalid
	}
	source, _ := json.Marshal(t.Source)
	turns, _ := json.Marshal(t.Turns)
	if t.Speakers == nil {
		t.Speakers = map[string]string{}
	}
	speakers, _ := json.Marshal(t.Speakers)
	return db.WithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO voice_transcripts(id,owner_user_id,title,source,language,turns,speakers,duration_ms,note_id,recording_item_id)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, t.ID, user, t.Title, source, t.Language, turns, speakers, t.DurationMS, t.NoteID, t.RecordingItemID)
		return err
	})
}

func (db *Database) VoiceTranscript(ctx context.Context, user, id string) (VoiceTranscript, error) {
	var t VoiceTranscript
	var source, turns, speakers []byte
	err := db.WithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT id,title,source,language,turns,speakers,duration_ms,note_id,recording_item_id,created_at
 FROM voice_transcripts WHERE id=$1 AND owner_user_id=$2`, id, user).Scan(&t.ID, &t.Title, &source, &t.Language, &turns, &speakers, &t.DurationMS, &t.NoteID, &t.RecordingItemID, &t.CreatedAt)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrSpaceNotFound
	}
	if err != nil {
		return t, err
	}
	_ = json.Unmarshal(source, &t.Source)
	_ = json.Unmarshal(turns, &t.Turns)
	_ = json.Unmarshal(speakers, &t.Speakers)
	return t, nil
}

// UpdateVoiceTranscript renames speakers (for this transcript only) and
// links the Note or recording made from it. Empty values leave fields alone.
func (db *Database) UpdateVoiceTranscript(ctx context.Context, user, id string, speakers map[string]string, noteID, recordingItemID string) error {
	if speakers != nil && !validSpeakerNames(speakers) || len(noteID) > 200 || len(recordingItemID) > 200 {
		return ErrSpaceInvalid
	}
	var names []byte
	if speakers != nil {
		names, _ = json.Marshal(speakers)
	}
	return db.WithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE voice_transcripts SET
 speakers=COALESCE($3::jsonb,speakers), note_id=COALESCE(NULLIF($4,''),note_id), recording_item_id=COALESCE(NULLIF($5,''),recording_item_id), updated_at=now()
 WHERE id=$1 AND owner_user_id=$2`, id, user, nullableJSON(names), noteID, recordingItemID)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return ErrSpaceNotFound
		}
		return nil
	})
}

func (db *Database) DeleteVoiceTranscript(ctx context.Context, user, id string) error {
	return db.WithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM voice_transcripts WHERE id=$1 AND owner_user_id=$2`, id, user)
		return err
	})
}

func nullableJSON(value []byte) any {
	if value == nil {
		return nil
	}
	return value
}

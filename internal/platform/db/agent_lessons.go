package db

import (
	"context"
	"database/sql"
	"encoding/json"
)

// AgentLessonDocuments is a person's Teach-mode progress: each lesson (keyed by its
// chat) and each course, as the app's own JSON.
type AgentLessonDocuments struct {
	Lessons []json.RawMessage `json:"lessons"`
	Courses []json.RawMessage `json:"courses"`
}

func (db *Database) ListAgentLessonDocuments(ctx context.Context, userID string) (AgentLessonDocuments, error) {
	out := AgentLessonDocuments{Lessons: []json.RawMessage{}, Courses: []json.RawMessage{}}
	err := db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT kind,document FROM agent_lesson_documents WHERE owner_user_id=$1 ORDER BY updated_at DESC`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var kind string
			var document json.RawMessage
			if err := rows.Scan(&kind, &document); err != nil {
				return err
			}
			if kind == "course" {
				out.Courses = append(out.Courses, document)
			} else {
				out.Lessons = append(out.Lessons, document)
			}
		}
		return rows.Err()
	})
	return out, err
}

func (db *Database) PutAgentLessonDocument(ctx context.Context, userID, kind, id string, document json.RawMessage) error {
	if (kind != "lesson" && kind != "course") || id == "" || len(id) > 256 || !validJSONObject(document) {
		return ErrSpaceInvalid
	}
	return db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO agent_lesson_documents(owner_user_id,kind,id,document) VALUES($1,$2,$3,$4)
			ON CONFLICT(owner_user_id,kind,id) DO UPDATE SET document=EXCLUDED.document,updated_at=NOW()`, userID, kind, id, []byte(document))
		return err
	})
}

func (db *Database) DeleteAgentLessonDocument(ctx context.Context, userID, kind, id string) error {
	return db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM agent_lesson_documents WHERE owner_user_id=$1 AND kind=$2 AND id=$3`, userID, kind, id)
		return err
	})
}

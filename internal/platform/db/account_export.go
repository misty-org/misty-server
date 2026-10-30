package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

type AccountExportSpace struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Role     string    `json:"role"`
	JoinedAt time.Time `json:"joined_at"`
}

type AccountExportProfile struct {
	ID            string    `json:"id"`
	Username      string    `json:"username"`
	Email         string    `json:"email"`
	AvatarVersion int64     `json:"avatar_version"`
	CreatedAt     time.Time `json:"created_at"`
}

type AccountExportJournal struct {
	Kind       string    `json:"kind"`
	ID         string    `json:"id"`
	SpaceID    string    `json:"space_id"`
	Title      string    `json:"title"`
	ACLVersion int64     `json:"acl_version"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type AccountExportAsset struct {
	Kind      string    `json:"kind"`
	ID        string    `json:"id"`
	ParentID  string    `json:"parent_id"`
	Filename  string    `json:"filename"`
	ObjectKey string    `json:"-"`
	MIMEType  string    `json:"mime_type"`
	ByteSize  int64     `json:"byte_size"`
	SHA256    string    `json:"sha256"`
	CreatedAt time.Time `json:"created_at"`
}

type AccountExportMessage struct {
	ID        string          `json:"id"`
	SpaceID   string          `json:"space_id"`
	Content   json.RawMessage `json:"content"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

type AccountPortableExport struct {
	FormatVersion int                    `json:"format_version"`
	ExportedAt    time.Time              `json:"exported_at"`
	Account       AccountExportProfile   `json:"account"`
	Settings      UserSettings           `json:"settings"`
	Spaces        []AccountExportSpace   `json:"spaces"`
	Journal       []AccountExportJournal `json:"journal"`
	Assets        []AccountExportAsset   `json:"assets"`
	Messages      []AccountExportMessage `json:"authored_messages"`
	Agents        []AccountExportAgent   `json:"agents"`
	Connections   []map[string]any       `json:"cloud_connections"`
}

// AccountPortableExport returns user-supplied and account-owned data without
// password hashes, sessions, OAuth secrets, internal object keys, or operator
// credentials. Journal CRDT and asset URLs are attached by the API after fresh
// authorization checks.
func (db *Database) AccountPortableExport(
	ctx context.Context, userID string,
) (*AccountPortableExport, error) {
	out := &AccountPortableExport{
		FormatVersion: 2,
		ExportedAt:    time.Now().UTC(),
		Spaces:        []AccountExportSpace{},
		Journal:       []AccountExportJournal{},
		Assets:        []AccountExportAsset{},
		Messages:      []AccountExportMessage{},
		Agents:        []AccountExportAgent{},
		Connections:   []map[string]any{},
	}
	err := db.WithRLSContext(ctx, ServiceRLSSettings(), func(tx *sql.Tx) error {
		if err := loadExportAccountProfile(ctx, tx, userID, out); err != nil {
			return err
		}
		if err := loadExportSettings(ctx, tx, userID, out); err != nil {
			return err
		}
		if err := loadExportSpaces(ctx, tx, userID, out); err != nil {
			return err
		}
		if err := loadExportJournal(ctx, tx, userID, out); err != nil {
			return err
		}
		if err := loadExportAssets(ctx, tx, userID, out); err != nil {
			return err
		}
		if err := loadExportMessages(ctx, tx, userID, out); err != nil {
			return err
		}
		if err := appendAccountAgentExport(ctx, tx, userID, out); err != nil {
			return err
		}
		return loadExportConnections(ctx, tx, userID, out)
	})
	return out, err
}

func scanExportQuery(
	ctx context.Context,
	tx *sql.Tx,
	query string,
	userID string,
	scan func(*sql.Rows) error,
) error {
	rows, err := tx.QueryContext(ctx, query, userID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

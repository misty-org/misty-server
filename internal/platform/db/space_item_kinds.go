package db

import (
	"context"
	"database/sql"
	"strings"
)

type SpaceItemKind struct {
	Name          string
	Section       string
	Table         string
	OwnerColumn   string
	TitleColumn   string
	DefaultTitle  string
	IDPrefix      string
	EventFamily   string
	Party         string
	ControlTable  string
	ControlColumn string
	ControlQueue  string
	AssetTable    string
}

var spaceItemKinds = []SpaceItemKind{
	{
		Name: "note", Section: "notes", Table: "space_notes", OwnerColumn: "creator_user_id",
		TitleColumn: "title_projection", DefaultTitle: "Untitled doc", IDPrefix: "note_",
		EventFamily: "note.", Party: "note-room",
		ControlTable: "space_note_control_outbox", ControlColumn: "note_id", ControlQueue: "note-control",
		AssetTable: "space_note_assets",
	},
}

const (
	SpaceItemNote = "note"
)

func SpaceItemKinds() []SpaceItemKind {
	return append([]SpaceItemKind(nil), spaceItemKinds...)
}

func SpaceItemKindNamed(name string) (SpaceItemKind, bool) {
	name = strings.TrimSpace(name)
	for _, kind := range spaceItemKinds {
		if kind.Name == name || kind.Section == name {
			return kind, true
		}
	}
	return SpaceItemKind{}, false
}

func spaceItemKindForEvent(eventType string) (SpaceItemKind, bool) {
	for _, kind := range spaceItemKinds {
		if strings.HasPrefix(eventType, kind.EventFamily) {
			return kind, true
		}
	}
	return SpaceItemKind{}, false
}

func spaceItemKindForTable(table string) (SpaceItemKind, bool) {
	for _, kind := range spaceItemKinds {
		if kind.Table == table {
			return kind, true
		}
	}
	return SpaceItemKind{}, false
}

func spaceItemKeyNames() string {
	names := make([]string, 0, len(spaceItemKinds))
	for _, kind := range spaceItemKinds {
		names = append(names, kind.Name)
	}
	return strings.Join(names, "|")
}

func (kind SpaceItemKind) enqueueControlTx(ctx context.Context, tx *sql.Tx, itemID, command string, payload map[string]any) error {
	switch kind.Name {
	case SpaceItemNote:
		return enqueueNoteControlTx(ctx, tx, itemID, command, payload)
	}
	return ErrSpaceInvalid
}

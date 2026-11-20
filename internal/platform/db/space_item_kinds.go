package db

import (
	"context"
	"database/sql"
	"strings"
)

// SpaceItemKind is one kind of collaborative document people and agents make in a Space. Today
// that is the doc (a Space note, which Lab notebooks are too). Each is a document in a room of
// the collaboration Worker, with a row of its own here. Every list that names the kinds (visibility, audiences, realtime
// event families, personal item keys, revocation, purge, export, history, search) reads this
// table, so a new kind is added here once instead of in each of them.
type SpaceItemKind struct {
	// Name is the kind as the API spells it everywhere one item is named: item keys
	// ("note:<id>"), the visibility route (/items/note/<id>), tickets' resource_type, history and
	// search results.
	Name string
	// Section is the kind's route section, /spaces/{space}/<section>, and its history path.
	Section string
	// Table holds the kind's rows; OwnerColumn names their creator.
	Table       string
	OwnerColumn string
	// TitleColumn is the column the item's title is read from.
	TitleColumn  string
	DefaultTitle string
	// IDPrefix starts every new item's id.
	IDPrefix string
	// EventFamily prefixes every realtime event about one item of this kind ("note.").
	EventFamily string
	// Party is the Worker's Durable Object binding, kebab-cased, that holds the item's room.
	Party string
	// ControlTable is the item's room command outbox, keyed by ControlColumn.
	ControlTable  string
	ControlColumn string
	// ControlQueue is the worker queue that delivers the room's commands.
	ControlQueue string
	// AssetTable holds the item's images, when the kind has them.
	AssetTable string
}

// spaceItemKinds is the registry. Order is the order kinds appear in lists and exports.
var spaceItemKinds = []SpaceItemKind{
	{
		Name: "note", Section: "notes", Table: "space_notes", OwnerColumn: "creator_user_id",
		TitleColumn: "title_projection", DefaultTitle: "Untitled doc", IDPrefix: "note_",
		EventFamily: "note.", Party: "note-room",
		ControlTable: "space_note_control_outbox", ControlColumn: "note_id", ControlQueue: "note-control",
		AssetTable: "space_note_assets",
	},
}

// Kind names, for callers that name one directly.
const (
	SpaceItemNote = "note"
)

// SpaceItemKinds lists every registered kind, in registry order.
func SpaceItemKinds() []SpaceItemKind {
	return append([]SpaceItemKind(nil), spaceItemKinds...)
}

// SpaceItemKindNamed finds a kind by its name ("note") or its section ("notes").
func SpaceItemKindNamed(name string) (SpaceItemKind, bool) {
	name = strings.TrimSpace(name)
	for _, kind := range spaceItemKinds {
		if kind.Name == name || kind.Section == name {
			return kind, true
		}
	}
	return SpaceItemKind{}, false
}

// spaceItemKindForEvent is the kind an event type belongs to, by its family.
func spaceItemKindForEvent(eventType string) (SpaceItemKind, bool) {
	for _, kind := range spaceItemKinds {
		if strings.HasPrefix(eventType, kind.EventFamily) {
			return kind, true
		}
	}
	return SpaceItemKind{}, false
}

// spaceItemKindForTable finds a kind by its table.
func spaceItemKindForTable(table string) (SpaceItemKind, bool) {
	for _, kind := range spaceItemKinds {
		if kind.Table == table {
			return kind, true
		}
	}
	return SpaceItemKind{}, false
}

// spaceItemKeyNames is the alternation of every kind's name, for item-key patterns.
func spaceItemKeyNames() string {
	names := make([]string, 0, len(spaceItemKinds))
	for _, kind := range spaceItemKinds {
		names = append(names, kind.Name)
	}
	return strings.Join(names, "|")
}

// enqueueControlTx queues one command for an item's room, in the kind's ordered outbox.
func (kind SpaceItemKind) enqueueControlTx(ctx context.Context, tx *sql.Tx, itemID, command string, payload map[string]any) error {
	switch kind.Name {
	case SpaceItemNote:
		return enqueueNoteControlTx(ctx, tx, itemID, command, payload)
	}
	return ErrSpaceInvalid
}

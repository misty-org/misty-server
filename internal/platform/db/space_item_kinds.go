package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
)

// SpaceItemKind is one kind of thing people and agents make in a Space: a doc, a whiteboard, a
// deck or a workbook. Each is a collaborative document in a room of the collaboration Worker,
// with a row of its own here. Every list that names the kinds (visibility, audiences, realtime
// event families, personal item keys, revocation, purge, export, history, search) reads this
// table, so a new kind is added here once instead of in each of them.
type SpaceItemKind struct {
	// Name is the kind as the API spells it everywhere one item is named: item keys
	// ("deck:<id>"), the visibility route (/items/deck/<id>), tickets' resource_type, history and
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
	// EventFamily prefixes every realtime event about one item of this kind ("deck.").
	EventFamily string
	// Party is the Worker's Durable Object binding, kebab-cased, that holds the item's room.
	Party string
	// ControlTable is the item's room command outbox, keyed by ControlColumn.
	ControlTable  string
	ControlColumn string
	// ControlQueue and PurgeQueue are the worker queues that deliver commands and remove purged
	// rows. Notes purge through their own lifecycle worker, so their PurgeQueue is empty.
	ControlQueue string
	PurgeQueue   string
	// CountColumn holds the item's size in the unit its level limit counts (slides, cells), or
	// is empty for kinds without such a limit.
	CountColumn string
	// AssetTable holds the item's images, when the kind has them.
	AssetTable string
	// Shared marks the kinds built entirely on the shared made-item code (space_made_items.go).
	// Notes and drawings came first and keep their own.
	Shared bool
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
	{
		Name: "drawing", Section: "drawings", Table: "space_drawings", OwnerColumn: "creator_user_id",
		TitleColumn: "title", DefaultTitle: "Untitled drawing", IDPrefix: "drawing_",
		EventFamily: "drawing.", Party: "drawing-room",
		ControlTable: "space_drawing_control_outbox", ControlColumn: "drawing_id",
		ControlQueue: "drawing-control", PurgeQueue: "drawing-purge",
		AssetTable: "space_drawing_assets",
	},
	{
		Name: "deck", Section: "decks", Table: "space_decks", OwnerColumn: "creator_user_id",
		TitleColumn: "title", DefaultTitle: "Untitled deck", IDPrefix: "deck_",
		EventFamily: "deck.", Party: "deck-room",
		ControlTable: "space_deck_control_outbox", ControlColumn: "deck_id",
		ControlQueue: "deck-control", PurgeQueue: "deck-purge",
		CountColumn: "slide_count", AssetTable: "space_deck_assets", Shared: true,
	},
	{
		Name: "workbook", Section: "workbooks", Table: "space_workbooks", OwnerColumn: "creator_user_id",
		TitleColumn: "title", DefaultTitle: "Untitled data", IDPrefix: "workbook_",
		EventFamily: "workbook.", Party: "workbook-room",
		ControlTable: "space_workbook_control_outbox", ControlColumn: "workbook_id",
		ControlQueue: "workbook-control", PurgeQueue: "workbook-purge",
		CountColumn: "cell_count", Shared: true,
	},
}

// Kind names, for callers that name one directly.
const (
	SpaceItemNote     = "note"
	SpaceItemDrawing  = "drawing"
	SpaceItemDeck     = "deck"
	SpaceItemWorkbook = "workbook"
)

// SpaceItemKinds lists every registered kind, in registry order.
func SpaceItemKinds() []SpaceItemKind {
	return append([]SpaceItemKind(nil), spaceItemKinds...)
}

// SpaceItemKindNamed finds a kind by its name ("deck") or its section ("decks").
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

// spaceItemKindForQueue finds a shared kind by one of its worker queues.
func spaceItemKindForQueue(queue string) (SpaceItemKind, bool) {
	for _, kind := range spaceItemKinds {
		if kind.Shared && (kind.ControlQueue == queue || kind.PurgeQueue == queue) {
			return kind, true
		}
	}
	return SpaceItemKind{}, false
}

// sharedSpaceItemKinds are the kinds built on space_made_items.go.
func sharedSpaceItemKinds() []SpaceItemKind {
	kinds := []SpaceItemKind{}
	for _, kind := range spaceItemKinds {
		if kind.Shared {
			kinds = append(kinds, kind)
		}
	}
	return kinds
}

// SpaceItemWorkerQueues are the worker queues the shared kinds add: each kind's control and purge.
func SpaceItemWorkerQueues() []string {
	queues := []string{}
	for _, kind := range sharedSpaceItemKinds() {
		queues = append(queues, kind.ControlQueue, kind.PurgeQueue)
	}
	return queues
}

// spaceItemKeyNames is the alternation of every kind's name, for item-key patterns.
func spaceItemKeyNames() string {
	names := make([]string, 0, len(spaceItemKinds))
	for _, kind := range spaceItemKinds {
		names = append(names, kind.Name)
	}
	return strings.Join(names, "|")
}

// enqueueControlTx queues one command for an item's room. Notes keep their ordered outbox and
// drawings their own; the shared kinds use their kind's outbox.
func (kind SpaceItemKind) enqueueControlTx(ctx context.Context, tx *sql.Tx, itemID, command string, payload map[string]any) error {
	switch kind.Name {
	case SpaceItemNote:
		return enqueueNoteControlTx(ctx, tx, itemID, command, payload)
	case SpaceItemDrawing:
		return enqueueDrawingControlTx(ctx, tx, itemID, command, payload)
	}
	if payload == nil {
		payload = map[string]any{}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	// Table and column names come from the registry, never from a request.
	_, err = tx.ExecContext(ctx,
		`INSERT INTO `+kind.ControlTable+`(id,`+kind.ControlColumn+`,command,payload) VALUES($1,$2,$3,$4)`,
		kind.Name+"ctl_"+uuid.NewString(), itemID, command, raw)
	return err
}

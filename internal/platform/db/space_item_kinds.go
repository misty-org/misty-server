package db

import (
	"strings"
)

type SpaceItemKind struct {
	Name         string
	Section      string
	Table        string
	OwnerColumn  string
	TitleColumn  string
	DefaultTitle string
	IDPrefix     string
	EventFamily  string
	AssetTable   string
}

var spaceItemKinds = []SpaceItemKind{
	{
		Name: "note", Section: "notes", Table: "space_notes", OwnerColumn: "creator_user_id",
		TitleColumn: "title_projection", DefaultTitle: "Untitled doc", IDPrefix: "note_",
		EventFamily: "note.", AssetTable: "space_note_assets",
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

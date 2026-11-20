package db

import (
	"regexp"
	"strings"
	"testing"
)

// Every registered kind's events have a visibility rule of their own: none falls through to the
// Space-wide families, which reach every member, or to the default, which reaches no one.
func TestEverySpaceItemKindHasAnEventVisibilityRule(t *testing.T) {
	for _, kind := range SpaceItemKinds() {
		for _, event := range []string{"created", "updated", "deleted"} {
			eventType := kind.EventFamily + event
			got, ok := spaceItemKindForEvent(eventType)
			if !ok || got.Name != kind.Name {
				t.Errorf("%s has no visibility rule (resolved to %q)", eventType, got.Name)
			}
			if spaceWideEvent(eventType) {
				t.Errorf("%s would reach every member as a Space-wide event", eventType)
			}
		}
		if _, ok := spaceItemKindForTable(kind.Table); !ok {
			t.Errorf("%s's table %s is not an audience table events can check", kind.Name, kind.Table)
		}
	}
}

// The registry is the one list: its names, sections, tables, rooms, outboxes and queues are all
// distinct, and every name a SQL string is built from is a plain identifier.
func TestSpaceItemKindRegistryIsConsistent(t *testing.T) {
	identifier := regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	seen := map[string]string{}
	for _, kind := range SpaceItemKinds() {
		for field, value := range map[string]string{
			"name": kind.Name, "section": kind.Section, "table": kind.Table, "party": kind.Party,
			"control table": kind.ControlTable, "control queue": kind.ControlQueue, "family": kind.EventFamily,
			"id prefix": kind.IDPrefix,
		} {
			if value == "" {
				t.Errorf("%s has no %s", kind.Name, field)
				continue
			}
			if other, dup := seen[value]; dup {
				t.Errorf("%s's %s %q is also %s", kind.Name, field, value, other)
			}
			seen[value] = kind.Name + " " + field
		}
		for _, name := range []string{kind.Table, kind.OwnerColumn, kind.TitleColumn, kind.ControlTable, kind.ControlColumn} {
			if !identifier.MatchString(name) {
				t.Errorf("%s: %q is not a plain SQL identifier", kind.Name, name)
			}
		}
		if kind.EventFamily != kind.Name+"." || kind.Party != kind.Name+"-room" || !strings.HasPrefix(kind.IDPrefix, kind.Name) {
			t.Errorf("%s's family, room or id prefix doesn't follow its name", kind.Name)
		}
		if found, ok := SpaceItemKindNamed(kind.Section); !ok || found.Name != kind.Name {
			t.Errorf("section %s does not find %s", kind.Section, kind.Name)
		}
		if visibilityTables[kind.Name][0] != kind.Table || audienceTables[kind.Name] != kind.Table {
			t.Errorf("%s is missing from the visibility or audience tables", kind.Name)
		}
		if !ValidSpaceItemKey(kind.Name + ":item_1") {
			t.Errorf("%s has no personal item key", kind.Name)
		}
	}
	if _, ok := SpaceItemKindNamed("note"); !ok {
		t.Error("note is not registered")
	}
	// Misty ships no Space apps of its own: whiteboards, decks and workbooks are gone.
	for _, name := range []string{"deck", "workbook", "drawing"} {
		if _, ok := SpaceItemKindNamed(name); ok {
			t.Errorf("%s is still registered", name)
		}
	}
	for _, kind := range SpaceItemKinds() {
		if !workerQueue(kind.ControlQueue) {
			t.Errorf("worker queue %s is not known to the worker hub", kind.ControlQueue)
		}
	}
}

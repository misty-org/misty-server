package api

import "testing"

func TestSearchKindsTakeEveryMadeKindByNameOrSection(t *testing.T) {
	all, ok := spaceSearchKinds("")
	if !ok || !all["message"] || !all["note"] {
		t.Fatalf("searching everything = %v %v", all, ok)
	}
	picked, ok := spaceSearchKinds("notes, message")
	if !ok || !picked["note"] || !picked["message"] {
		t.Fatalf("picked kinds = %v %v", picked, ok)
	}
	for _, removed := range []string{"note,secrets", "drawing", "decks", "workbook"} {
		if _, ok := spaceSearchKinds(removed); ok {
			t.Fatalf("%q was accepted", removed)
		}
	}
}

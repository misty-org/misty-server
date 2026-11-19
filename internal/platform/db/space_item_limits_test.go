package db

import (
	"encoding/json"
	"testing"
)

// history_days, deck_slides and workbook_cells read from MISTY_SPACE_LEVELS like the other
// limits: unset or null is unlimited, a number is the limit, and less than 1 is refused.
func TestSpaceLevelItemLimits(t *testing.T) {
	var config SpaceLevelConfig
	raw := `{"thresholds":[2],"levels":[
		{"members":5},
		{"members":10,"history_days":30,"deck_slides":200,"workbook_cells":100000}]}`
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		t.Fatal(err)
	}
	if err := config.validate(); err != nil {
		t.Fatal(err)
	}
	base := config.levelFor(0).Limits
	if base.HistoryDays != nil || base.DeckSlides != nil || base.WorkbookCells != nil {
		t.Fatalf("unset item limits are not unlimited: %+v", base)
	}
	boosted := config.levelFor(2).Limits
	if boosted.HistoryDays == nil || *boosted.HistoryDays != 30 || boosted.DeckSlides == nil || *boosted.DeckSlides != 200 ||
		boosted.WorkbookCells == nil || *boosted.WorkbookCells != 100000 {
		t.Fatalf("level 1 item limits = %+v", boosted)
	}
	limits := SpaceItemLimits{HistoryDays: boosted.HistoryDays, DeckSlides: boosted.DeckSlides, WorkbookCells: boosted.WorkbookCells}
	deck, _ := SpaceItemKindNamed(SpaceItemDeck)
	workbook, _ := SpaceItemKindNamed(SpaceItemWorkbook)
	drawing, _ := SpaceItemKindNamed(SpaceItemDrawing)
	if *limits.countLimit(deck) != 200 || *limits.countLimit(workbook) != 100000 || limits.countLimit(drawing) != nil {
		t.Fatal("count limits don't follow their kinds")
	}
	for _, bad := range []string{
		`{"thresholds":[],"levels":[{"history_days":0}]}`,
		`{"thresholds":[],"levels":[{"deck_slides":-1}]}`,
		`{"thresholds":[],"levels":[{"workbook_cells":0}]}`,
	} {
		var config SpaceLevelConfig
		if err := json.Unmarshal([]byte(bad), &config); err != nil {
			t.Fatal(err)
		}
		if config.validate() == nil {
			t.Errorf("%s was accepted", bad)
		}
	}
}

func TestMadeItemCountReadsItsKindsField(t *testing.T) {
	slides, cells := int64(12), int64(3400)
	if madeItemCount(&SpaceMadeItem{SlideCount: &slides}) != 12 || madeItemCount(&SpaceMadeItem{CellCount: &cells}) != 3400 ||
		madeItemCount(&SpaceMadeItem{}) != 0 {
		t.Fatal("made item counts")
	}
	deck, _ := SpaceItemKindNamed(SpaceItemDeck)
	if title, err := NormalizeMadeItemTitle(deck, "  "); err != nil || title != "Untitled deck" {
		t.Fatalf("empty deck title = %q %v", title, err)
	}
	long := make([]rune, 201)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := NormalizeMadeItemTitle(deck, string(long)); err == nil {
		t.Fatal("a 201-character title was accepted")
	}
}

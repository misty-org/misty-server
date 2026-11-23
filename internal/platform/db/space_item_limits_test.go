package db

import (
	"encoding/json"
	"testing"
)

func TestSpaceLevelItemLimits(t *testing.T) {
	var config SpaceLevelConfig
	raw := `{"thresholds":[2],"levels":[
		{"members":5},
		{"members":10,"history_days":30}]}`
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		t.Fatal(err)
	}
	if err := config.validate(); err != nil {
		t.Fatal(err)
	}
	if base := config.levelFor(0).Limits; base.HistoryDays != nil {
		t.Fatalf("unset item limits are not unlimited: %+v", base)
	}
	if boosted := config.levelFor(2).Limits; boosted.HistoryDays == nil || *boosted.HistoryDays != 30 {
		t.Fatalf("level 1 item limits = %+v", boosted)
	}
	var bad SpaceLevelConfig
	if err := json.Unmarshal([]byte(`{"thresholds":[],"levels":[{"history_days":0}]}`), &bad); err != nil {
		t.Fatal(err)
	}
	if bad.validate() == nil {
		t.Error("history_days 0 was accepted")
	}
}

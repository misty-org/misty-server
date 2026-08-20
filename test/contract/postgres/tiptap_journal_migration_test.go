package db

import (
	"os"
	"strings"
	"testing"
)

func TestTipTapJournalResetIsNativeOnlyAndRetryable(t *testing.T) {
	raw, err := os.ReadFile("../../../internal/platform/postgres/migrations/20261029000000_tiptap_journal.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.ToLower(string(raw))
	for _, required := range []string{
		"update space_notes",
		"set lifecycle_state='deleting'",
		"update space_note_assets",
		"insert into space_note_control_outbox",
		"where o.note_id=n.id and o.command='purge' and o.delivered_at is null",
		"on conflict (id) do nothing",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("reset migration is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"delete from space_notes",
		"delete from space_note_assets",
		"notion_pages",
		"provider_resources",
	} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("reset migration contains unsafe connector or eager-delete statement %q", forbidden)
		}
	}
}

package db

import (
	"context"
	"database/sql"
	"regexp"
	"strings"
)

var (
	noteImagePattern = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	noteLinkPattern  = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
	noteMarkPattern  = regexp.MustCompile("[`*_>#~-]+")
	noteSpacePattern = regexp.MustCompile(`\s+`)
)

func noteMarkdownPlainText(markdown string) string {
	text := noteImagePattern.ReplaceAllString(markdown, " ")
	text = noteLinkPattern.ReplaceAllString(text, "$1")
	text = noteMarkPattern.ReplaceAllString(text, " ")
	return strings.TrimSpace(noteSpacePattern.ReplaceAllString(text, " "))
}

func writeNoteContentTx(ctx context.Context, tx *sql.Tx, noteID, creatorUserID, spaceID, title, markdown string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE space_notes
		SET title_projection=$1,markdown_projection=$2,plain_text_projection=$3,
		    collaboration_revision=collaboration_revision+1,updated_at=NOW()
		WHERE id=$4`, title, markdown, noteMarkdownPlainText(markdown), noteID); err != nil {
		return err
	}
	none := int64(0)
	return applyNoteContentBytesTx(ctx, tx, spaceID, creatorUserID, noteID, &none, &none)
}

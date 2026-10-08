package db

import (
	"context"
	"database/sql"
	"strings"
)

// Limits on what one template carries, so a large Space can't produce an
// oversized template or a slow create.
const (
	templateTaskLimit       = 50
	templateNoteLimit       = 20
	templateCollectionLimit = 50
)

// TemplateParts selects what a captured template includes.
type TemplateParts struct {
	Tasks       bool `json:"tasks"`
	Notes       bool `json:"notes"`
	Collections bool `json:"collections"`
}

// AllTemplateParts captures everything a template can carry.
var AllTemplateParts = TemplateParts{Tasks: true, Notes: true, Collections: true}

// captureSpaceStructureTx copies a Space's structure, never its content: task
// titles without assignees or dates, outlines (headings) of notes the caller
// wrote, and collection names. Files, messages and members stay behind. Each
// part respects the caller's permissions in the source Space.
func captureSpaceStructureTx(ctx context.Context, tx *sql.Tx, userID, spaceID string, parts TemplateParts) (templateDefinition, error) {
	definition := templateDefinition{}
	if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
		return definition, err
	}
	if parts.Tasks {
		allowed, err := hasSpacePermissionTx(ctx, tx, userID, spaceID, PermissionTasksView)
		if err != nil {
			return definition, err
		}
		if allowed {
			if definition.Tasks, err = queryStrings(ctx, tx, `SELECT title FROM space_tasks
				WHERE space_id=$1 AND archived_at IS NULL AND audience_kind='space'
				ORDER BY rank, task_number LIMIT $2`, spaceID, templateTaskLimit); err != nil {
				return definition, err
			}
		}
	}
	if parts.Notes {
		// Only the caller's own notes: someone else's note may not be shared with them.
		rows, err := tx.QueryContext(ctx, `SELECT title_projection, COALESCE(markdown_projection,'')
			FROM space_notes WHERE space_id=$1 AND creator_user_id=$2 AND lifecycle_state='active'
			AND audience_kind='space' ORDER BY created_at LIMIT $3`, spaceID, userID, templateNoteLimit)
		if err != nil {
			return definition, err
		}
		defer rows.Close()
		for rows.Next() {
			var title, markdown string
			if err := rows.Scan(&title, &markdown); err != nil {
				return definition, err
			}
			if title = strings.TrimSpace(title); title == "" {
				title = "Untitled"
			}
			definition.Notes = append(definition.Notes, templateNote{Title: title, Markdown: noteOutline(title, markdown)})
		}
		if err := rows.Err(); err != nil {
			return definition, err
		}
	}
	if parts.Collections {
		allowed, err := hasSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryView)
		if err != nil {
			return definition, err
		}
		if allowed {
			if definition.Collections, err = queryStrings(ctx, tx, `SELECT name FROM space_albums
				WHERE space_id=$1 ORDER BY position, created_at, id LIMIT $2`, spaceID, templateCollectionLimit); err != nil {
				return definition, err
			}
		}
	}
	definition.finish()
	return definition, nil
}

// noteOutline keeps a note's title and headings, dropping its body text.
func noteOutline(title, markdown string) string {
	var outline strings.Builder
	outline.WriteString("# " + title + "\n")
	for index, line := range strings.Split(markdown, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "#") {
			continue
		}
		// The note's own title heading is already written.
		if index == 0 && strings.TrimSpace(strings.TrimLeft(line, "#")) == title {
			continue
		}
		outline.WriteString("\n" + line + "\n")
	}
	return outline.String()
}

func queryStrings(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]string, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []string{}
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

// SpacePerson is someone the caller already shares an active Space with.
type SpacePerson struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
	Email  string `json:"email"`
}

// SpacePeople lists the people the caller shares active Spaces with, most
// recently joined first, so a new Space can invite them in one click.
func (db *Database) SpacePeople(ctx context.Context, userID string) ([]SpacePerson, error) {
	people := []SpacePerson{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT u.id,u.name,u.email
			FROM space_members mine
			JOIN spaces s ON s.id=mine.space_id AND s.lifecycle_state='active'
			JOIN space_members other ON other.space_id=mine.space_id AND other.user_id<>mine.user_id
			JOIN users u ON u.id=other.user_id
			WHERE mine.user_id=$1
			GROUP BY u.id,u.name,u.email
			ORDER BY max(other.joined_at) DESC, u.id
			LIMIT 24`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var person SpacePerson
			if err := rows.Scan(&person.UserID, &person.Name, &person.Email); err != nil {
				return err
			}
			people = append(people, person)
		}
		return rows.Err()
	})
	return people, err
}

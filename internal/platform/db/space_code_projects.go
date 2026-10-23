package db

import (
	"context"
	"database/sql"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// SpaceCodeProject is a coding project someone linked to a Space. Its files
// stay on the owner's device.
type SpaceCodeProject struct {
	ID          string    `json:"id"`
	SpaceID     string    `json:"space_id"`
	OwnerUserID string    `json:"owner_user_id"`
	OwnerName   string    `json:"owner_name"`
	ProjectID   string    `json:"project_id"`
	DeviceID    string    `json:"device_id"`
	Name        string    `json:"name"`
	GitHubRepo  string    `json:"github_repo"`
	LinkedAt    time.Time `json:"linked_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	// Visibility is "public" or "private": a private project shows only to its owner.
	Visibility string `json:"visibility"`
}

// SpaceCodeProjectInput links one of the caller's projects, or updates the link.
type SpaceCodeProjectInput struct {
	ProjectID  string `json:"project_id"`
	DeviceID   string `json:"device_id"`
	Name       string `json:"name"`
	GitHubRepo string `json:"github_repo"`
}

var githubRepoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}/[A-Za-z0-9_.-]{1,100}$`)

const spaceCodeProjectColumns = `p.id,p.space_id,p.owner_user_id,COALESCE(u.username,''),p.project_id,p.device_id,p.name,p.github_repo,p.linked_at,p.updated_at,p.visibility`

func scanSpaceCodeProject(row interface{ Scan(...any) error }, out *SpaceCodeProject) error {
	return row.Scan(&out.ID, &out.SpaceID, &out.OwnerUserID, &out.OwnerName, &out.ProjectID, &out.DeviceID, &out.Name, &out.GitHubRepo, &out.LinkedAt, &out.UpdatedAt, &out.Visibility)
}

// LinkSpaceCodeProject links one of the caller's projects to a Space they
// belong to. Linking the same project again updates its name and repository.
func (db *Database) LinkSpaceCodeProject(ctx context.Context, userID, spaceID string, input SpaceCodeProjectInput) (*SpaceCodeProject, error) {
	input.ProjectID, input.DeviceID = strings.TrimSpace(input.ProjectID), strings.TrimSpace(input.DeviceID)
	input.Name, input.GitHubRepo = strings.TrimSpace(input.Name), strings.TrimSpace(input.GitHubRepo)
	if input.ProjectID == "" || len(input.ProjectID) > 200 || len(input.DeviceID) > 200 || input.Name == "" || len([]rune(input.Name)) > 120 ||
		(input.GitHubRepo != "" && !githubRepoPattern.MatchString(input.GitHubRepo)) {
		return nil, ErrSpaceInvalid
	}
	out := &SpaceCodeProject{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		var id string
		if err := tx.QueryRowContext(ctx, `INSERT INTO space_code_projects(id,space_id,owner_user_id,project_id,device_id,name,github_repo)
			VALUES($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (space_id,owner_user_id,project_id) DO UPDATE SET device_id=EXCLUDED.device_id,name=EXCLUDED.name,
			github_repo=EXCLUDED.github_repo,updated_at=NOW()
			RETURNING id`, "codeproject_"+uuid.NewString(), spaceID, userID, input.ProjectID, input.DeviceID, input.Name, input.GitHubRepo).Scan(&id); err != nil {
			return err
		}
		if err := scanSpaceCodeProject(tx.QueryRowContext(ctx, `SELECT `+spaceCodeProjectColumns+` FROM space_code_projects p
			LEFT JOIN users u ON u.id=p.owner_user_id WHERE p.id=$1`, id), out); err != nil {
			return err
		}
		_, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "code_project.linked", id, map[string]any{"project": out})
		return err
	})
	return out, err
}

// UnlinkSpaceCodeProject removes a project from a Space. Its owner or the
// Space's owner may; the files on the device are untouched.
func (db *Database) UnlinkSpaceCodeProject(ctx context.Context, userID, spaceID, linkID string) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		role, err := requireSpaceMemberTx(ctx, tx, spaceID, userID)
		if err != nil {
			return err
		}
		// The Space's owner may unlink a public project, never someone's private one.
		result, err := tx.ExecContext(ctx, `DELETE FROM space_code_projects WHERE id=$1 AND space_id=$2 AND (owner_user_id=$3 OR ($4 AND visibility='public'))`, linkID, spaceID, userID, role == "owner")
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil || n == 0 {
			if err == nil {
				err = ErrSpaceNotFound
			}
			return err
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "code_project.unlinked", linkID, map[string]any{"id": linkID})
		return err
	})
}

// SpaceCodeProjects lists the projects linked to a Space, most recently
// updated first.
func (db *Database) SpaceCodeProjects(ctx context.Context, userID, spaceID string) ([]SpaceCodeProject, error) {
	items := []SpaceCodeProject{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+spaceCodeProjectColumns+` FROM space_code_projects p
			LEFT JOIN users u ON u.id=p.owner_user_id WHERE p.space_id=$1 AND (p.visibility='public' OR p.owner_user_id=$2)
			ORDER BY p.updated_at DESC,p.id LIMIT 200`, spaceID, itemViewer(ctx, userID))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item SpaceCodeProject
			if err := scanSpaceCodeProject(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

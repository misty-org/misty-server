package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"
)

const MaxLibraryEditVersions = 50

type LibraryCrop struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type LibraryTrim struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

type LibraryMarkupPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type LibraryMarkupElement struct {
	Kind      string               `json:"kind"`
	Points    []LibraryMarkupPoint `json:"points,omitempty"`
	X         float64              `json:"x,omitempty"`
	Y         float64              `json:"y,omitempty"`
	Width     float64              `json:"width,omitempty"`
	Height    float64              `json:"height,omitempty"`
	Color     string               `json:"color"`
	LineWidth float64              `json:"line_width"`
	Opacity   float64              `json:"opacity"`
	Text      string               `json:"text,omitempty"`
}

type LibraryEditDefinition struct {
	Rotation       int                    `json:"rotation"`
	FlipHorizontal bool                   `json:"flip_horizontal"`
	FlipVertical   bool                   `json:"flip_vertical"`
	AutoEnhance    bool                   `json:"auto_enhance"`
	Filter         string                 `json:"filter"`
	Brightness     float64                `json:"brightness"`
	Contrast       float64                `json:"contrast"`
	Saturation     float64                `json:"saturation"`
	Grayscale      float64                `json:"grayscale"`
	Exposure       float64                `json:"exposure"`
	Brilliance     float64                `json:"brilliance"`
	Highlights     float64                `json:"highlights"`
	Shadows        float64                `json:"shadows"`
	BlackPoint     float64                `json:"black_point"`
	Vibrance       float64                `json:"vibrance"`
	Warmth         float64                `json:"warmth"`
	Tint           float64                `json:"tint"`
	Sharpness      float64                `json:"sharpness"`
	Definition     float64                `json:"definition"`
	NoiseReduction float64                `json:"noise_reduction"`
	Vignette       float64                `json:"vignette"`
	Straighten     float64                `json:"straighten"`
	Markup         []LibraryMarkupElement `json:"markup,omitempty"`
	Mute           bool                   `json:"mute"`
	PlaybackSpeed  float64                `json:"playback_speed"`
	Crop           *LibraryCrop           `json:"crop,omitempty"`
	Trim           *LibraryTrim           `json:"trim,omitempty"`
}

type LibraryEditVersion struct {
	ID              string                `json:"id"`
	SpaceLibraryID  string                `json:"space_library_item_id"`
	ParentVersionID string                `json:"parent_version_id,omitempty"`
	CreatedByUserID string                `json:"created_by_user_id"`
	Definition      LibraryEditDefinition `json:"edit_definition"`
	LifecycleState  string                `json:"lifecycle_state"`
	RenditionState  string                `json:"rendition_state"`
	RenditionMIME   string                `json:"rendition_mime_type,omitempty"`
	RenditionBytes  int64                 `json:"rendition_byte_size,omitempty"`
	RenditionError  string                `json:"rendition_error_code,omitempty"`
	VersionNumber   int64                 `json:"version_number"`
	IsCurrent       bool                  `json:"is_current"`
	CreatedAt       time.Time             `json:"created_at"`
	RenditionAt     time.Time             `json:"rendition_updated_at"`
	DeletedAt       *time.Time            `json:"deleted_at,omitempty"`
}

type LibraryEditResult struct {
	Item *SpaceLibraryItem   `json:"item"`
	Edit *LibraryEditVersion `json:"edit,omitempty"`
}

func DefaultLibraryEditDefinition() LibraryEditDefinition {
	return LibraryEditDefinition{Brightness: 1, Contrast: 1, Saturation: 1, PlaybackSpeed: 1}
}

func (definition LibraryEditDefinition) Validate(mimeType string) error {
	if definition.PlaybackSpeed == 0 {
		definition.PlaybackSpeed = 1
	}
	validFilter := definition.Filter == "" || definition.Filter == "vivid" || definition.Filter == "dramatic" || definition.Filter == "warm" || definition.Filter == "cool" || definition.Filter == "mono" || definition.Filter == "noir"
	if !validFilter || definition.Rotation != 0 && definition.Rotation != 90 && definition.Rotation != 180 && definition.Rotation != 270 ||
		!finiteRange(definition.Brightness, 0, 3) || !finiteRange(definition.Contrast, 0, 3) || !finiteRange(definition.Saturation, 0, 3) || !finiteRange(definition.Grayscale, 0, 1) ||
		!finiteRange(definition.Exposure, -2, 2) || !finiteRange(definition.Brilliance, -1, 1) || !finiteRange(definition.Highlights, -1, 1) || !finiteRange(definition.Shadows, -1, 1) ||
		!finiteRange(definition.BlackPoint, -1, 1) || !finiteRange(definition.Vibrance, -1, 1) || !finiteRange(definition.Warmth, -1, 1) || !finiteRange(definition.Tint, -1, 1) ||
		!finiteRange(definition.Sharpness, 0, 2) || !finiteRange(definition.Definition, 0, 2) || !finiteRange(definition.NoiseReduction, 0, 1) || !finiteRange(definition.Vignette, 0, 1) ||
		!finiteRange(definition.Straighten, -45, 45) || !finiteRange(definition.PlaybackSpeed, .5, 2) {
		return ErrLibraryInvalid
	}
	if (definition.Mute || definition.PlaybackSpeed != 1) && !strings.HasPrefix(mimeType, "video/") {
		return ErrLibraryInvalid
	}
	if definition.Crop != nil {
		crop := definition.Crop
		if !finiteRange(crop.X, 0, 1) || !finiteRange(crop.Y, 0, 1) || !finiteRange(crop.Width, 0.01, 1) || !finiteRange(crop.Height, 0.01, 1) || crop.X+crop.Width > 1.000001 || crop.Y+crop.Height > 1.000001 {
			return ErrLibraryInvalid
		}
	}
	if definition.Trim != nil {
		if len(mimeType) < 6 || mimeType[:6] != "video/" || math.IsNaN(definition.Trim.Start) || math.IsNaN(definition.Trim.End) || definition.Trim.Start < 0 || definition.Trim.End <= definition.Trim.Start || definition.Trim.End > 86400 {
			return ErrLibraryInvalid
		}
	}
	if len(definition.Markup) > 16 {
		return ErrLibraryInvalid
	}
	markupCost := 0
	for _, element := range definition.Markup {
		if !validLibraryMarkupElement(element) {
			return ErrLibraryInvalid
		}
		if element.Kind == "cleanup" && !strings.HasPrefix(mimeType, "image/") {
			return ErrLibraryInvalid
		}
		if element.Kind == "text" {
			markupCost += len(element.Text) * 24
		} else if element.Kind == "rectangle" {
			markupCost += 4
		} else {
			markupCost += len(element.Points)
		}
	}
	if markupCost > 1024 {
		return ErrLibraryInvalid
	}
	return nil
}

func validLibraryMarkupElement(element LibraryMarkupElement) bool {
	if !libraryMarkupColor(element.Color) || !finiteRange(element.LineWidth, .001, .1) || !finiteRange(element.Opacity, .05, 1) {
		return false
	}
	switch element.Kind {
	case "stroke", "highlight":
		if len(element.Points) < 2 || len(element.Points) > 64 {
			return false
		}
		for _, point := range element.Points {
			if !finiteRange(point.X, 0, 1) || !finiteRange(point.Y, 0, 1) {
				return false
			}
		}
		return true
	case "rectangle", "cleanup":
		minimum := .001
		if element.Kind == "cleanup" {
			minimum = .01
		}
		return finiteRange(element.X, 0, 1) && finiteRange(element.Y, 0, 1) && finiteRange(element.Width, minimum, 1) && finiteRange(element.Height, minimum, 1) && element.X+element.Width <= 1.000001 && element.Y+element.Height <= 1.000001
	case "text":
		return finiteRange(element.X, 0, 1) && finiteRange(element.Y, 0, 1) && len(element.Text) > 0 && len(element.Text) <= 40 && libraryMarkupText(element.Text)
	default:
		return false
	}
}

func libraryMarkupColor(value string) bool {
	if len(value) != 7 || value[0] != '#' {
		return false
	}
	for _, character := range value[1:] {
		if character < '0' || character > '9' && character < 'A' || character > 'F' && character < 'a' || character > 'f' {
			return false
		}
	}
	return true
}

func libraryMarkupText(value string) bool {
	for _, character := range value {
		if character < 32 || character > 126 || strings.ContainsRune("'\\:%[];%", character) {
			return false
		}
	}
	return strings.TrimSpace(value) != ""
}

func finiteRange(value, minimum, maximum float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= minimum && value <= maximum
}

func (db *Database) LibraryEditVersions(ctx context.Context, userID, spaceID, itemID string) ([]LibraryEditVersion, error) {
	versions := []LibraryEditVersion{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryView); err != nil {
			return err
		}
		if err := requireLibraryItemAudienceTx(ctx, tx, userID, spaceID, itemID); err != nil {
			return err
		}
		var currentID string
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(current_edit_version_id,'') FROM space_library_items WHERE id=$1 AND space_id=$2`, itemID, spaceID).Scan(&currentID); errors.Is(err, sql.ErrNoRows) {
			return ErrLibraryNotFound
		} else if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT id,space_library_item_id,COALESCE(parent_version_id,''),created_by_user_id,edit_definition,lifecycle_state,rendition_state,rendition_mime_type,COALESCE(rendition_byte_size,0),rendition_error_code,version_number,created_at,rendition_updated_at,deleted_at FROM library_item_versions WHERE space_library_item_id=$1 AND lifecycle_state='ready' ORDER BY version_number DESC`, itemID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var version LibraryEditVersion
			var raw []byte
			if err := rows.Scan(&version.ID, &version.SpaceLibraryID, &version.ParentVersionID, &version.CreatedByUserID, &raw, &version.LifecycleState, &version.RenditionState, &version.RenditionMIME, &version.RenditionBytes, &version.RenditionError, &version.VersionNumber, &version.CreatedAt, &version.RenditionAt, &version.DeletedAt); err != nil {
				return err
			}
			if err := json.Unmarshal(raw, &version.Definition); err != nil {
				return err
			}
			if version.Definition.PlaybackSpeed == 0 {
				version.Definition.PlaybackSpeed = 1
			}
			version.IsCurrent = version.ID == currentID
			versions = append(versions, version)
		}
		return rows.Err()
	})
	return versions, err
}

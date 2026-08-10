package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

type SpaceRoadmapFieldDefinition struct {
	ID       string   `json:"id"`
	Label    string   `json:"label"`
	Type     string   `json:"type"`
	Options  []string `json:"options,omitempty"`
	Archived bool     `json:"archived,omitempty"`
}

var roadmapFieldIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

func decodeRoadmapFieldSchema(raw json.RawMessage) ([]SpaceRoadmapFieldDefinition, bool) {
	if len(raw) == 0 {
		return []SpaceRoadmapFieldDefinition{}, true
	}
	var fields []SpaceRoadmapFieldDefinition
	if len(raw) > 32768 || json.Unmarshal(raw, &fields) != nil || len(fields) > 20 {
		return nil, false
	}
	seen := map[string]bool{}
	validTypes := map[string]bool{"short_text": true, "long_text": true, "number": true, "date": true, "url": true, "select": true, "checkbox": true}
	for _, field := range fields {
		field.ID, field.Label = strings.TrimSpace(field.ID), strings.TrimSpace(field.Label)
		if !roadmapFieldIDPattern.MatchString(field.ID) || seen[field.ID] || !validTypes[field.Type] || field.Label == "" || len([]rune(field.Label)) > 80 {
			return nil, false
		}
		seen[field.ID] = true
		if field.Type != "select" && len(field.Options) != 0 || len(field.Options) > 50 {
			return nil, false
		}
		optionSeen := map[string]bool{}
		for _, option := range field.Options {
			option = strings.TrimSpace(option)
			if option == "" || len([]rune(option)) > 80 || optionSeen[option] {
				return nil, false
			}
			optionSeen[option] = true
		}
	}
	return fields, true
}

func validRoadmapDefinitionUpdate(previous, next []SpaceRoadmapFieldDefinition) bool {
	previousByID := map[string]SpaceRoadmapFieldDefinition{}
	nextByID := map[string]SpaceRoadmapFieldDefinition{}
	for _, field := range previous {
		previousByID[field.ID] = field
	}
	for _, field := range next {
		nextByID[field.ID] = field
		if old, ok := previousByID[field.ID]; ok && old.Type != field.Type {
			return false
		}
	}
	for id := range previousByID {
		if _, ok := nextByID[id]; !ok {
			return false
		}
	}
	return true
}

func validateRoadmapFieldValues(raw json.RawMessage, fields []SpaceRoadmapFieldDefinition) bool {
	if len(raw) == 0 {
		return true
	}
	if len(raw) > 65536 {
		return false
	}
	var values map[string]any
	if json.Unmarshal(raw, &values) != nil {
		return false
	}
	byID := map[string]SpaceRoadmapFieldDefinition{}
	for _, field := range fields {
		byID[field.ID] = field
	}
	for key, value := range values {
		field, ok := byID[key]
		if !ok {
			return false
		}
		switch field.Type {
		case "number":
			if _, ok := value.(float64); !ok {
				return false
			}
		case "checkbox":
			if _, ok := value.(bool); !ok {
				return false
			}
		default:
			text, ok := value.(string)
			if !ok || len([]rune(text)) > 20000 {
				return false
			}
			if field.Type == "select" && text != "" {
				found := false
				for _, option := range field.Options {
					if option == text {
						found = true
						break
					}
				}
				if !found {
					return false
				}
			}
		}
	}
	return true
}

func validRoadmapNodeKind(kind string) bool {
	return kind == "risk" || kind == "decision" || kind == "metric" || kind == "note" || kind == "custom"
}

func (db *Database) SpaceRoadmapNodeDefinitions(ctx context.Context, userID, spaceID string) ([]SpaceRoadmapNodeDefinition, error) {
	items := []SpaceRoadmapNodeDefinition{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionTasksView); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+roadmapNodeDefinitionColumns+` FROM space_roadmap_node_definitions WHERE space_id=$1 AND archived_at IS NULL ORDER BY name,id`, spaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item SpaceRoadmapNodeDefinition
			if err := scanSpaceRoadmapNodeDefinition(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func normalizeRoadmapNodeDefinition(item *SpaceRoadmapNodeDefinition) ([]SpaceRoadmapFieldDefinition, bool) {
	item.Name = strings.TrimSpace(item.Name)
	item.Description = strings.TrimSpace(item.Description)
	item.Icon = strings.TrimSpace(item.Icon)
	item.Color = strings.TrimSpace(item.Color)
	colors := map[string]bool{"slate": true, "blue": true, "cyan": true, "emerald": true, "amber": true, "orange": true, "rose": true, "violet": true}
	fields, fieldsOK := decodeRoadmapFieldSchema(item.FieldSchema)
	return fields, validRoadmapText(item.Name, 120) && len([]rune(item.Description)) <= 2000 && item.Icon != "" && len([]rune(item.Icon)) <= 80 && colors[item.Color] && fieldsOK
}

func (db *Database) CreateSpaceRoadmapNodeDefinition(ctx context.Context, userID, spaceID string, item SpaceRoadmapNodeDefinition) (*SpaceRoadmapNodeDefinition, error) {
	if _, ok := normalizeRoadmapNodeDefinition(&item); !ok {
		return nil, ErrSpaceInvalid
	}
	item.ID, item.SpaceID, item.CreatedByUserID = "roadmap_node_definition_"+uuid.NewString(), spaceID, userID
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionTasksManage); err != nil {
			return err
		}
		if err := scanSpaceRoadmapNodeDefinition(tx.QueryRowContext(ctx, `INSERT INTO space_roadmap_node_definitions(id,space_id,name,description,icon,color,agenda_visible,field_schema,created_by_user_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING `+roadmapNodeDefinitionColumns, item.ID, spaceID, item.Name, item.Description, item.Icon, item.Color, item.AgendaVisible, item.FieldSchema, userID), &item); err != nil {
			return err
		}
		_, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "roadmap.node_definition.created", item.ID, map[string]any{"definition_id": item.ID})
		return err
	})
	return &item, err
}

func (db *Database) UpdateSpaceRoadmapNodeDefinition(ctx context.Context, userID, spaceID, definitionID string, item SpaceRoadmapNodeDefinition, expected int64) (*SpaceRoadmapNodeDefinition, error) {
	nextFields, ok := normalizeRoadmapNodeDefinition(&item)
	if !ok || expected < 1 {
		return nil, ErrSpaceInvalid
	}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionTasksManage); err != nil {
			return err
		}
		var previousRaw json.RawMessage
		if err := tx.QueryRowContext(ctx, `SELECT field_schema FROM space_roadmap_node_definitions WHERE id=$1 AND space_id=$2 AND archived_at IS NULL`, definitionID, spaceID).Scan(&previousRaw); errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceNotFound
		} else if err != nil {
			return err
		}
		previousFields, valid := decodeRoadmapFieldSchema(previousRaw)
		if !valid || !validRoadmapDefinitionUpdate(previousFields, nextFields) {
			return ErrSpaceInvalid
		}
		err := scanSpaceRoadmapNodeDefinition(tx.QueryRowContext(ctx, `UPDATE space_roadmap_node_definitions SET name=$1,description=$2,icon=$3,color=$4,agenda_visible=$5,field_schema=$6,version=version+1,updated_at=NOW() WHERE id=$7 AND space_id=$8 AND archived_at IS NULL AND version=$9 RETURNING `+roadmapNodeDefinitionColumns, item.Name, item.Description, item.Icon, item.Color, item.AgendaVisible, item.FieldSchema, definitionID, spaceID, expected), &item)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceConflict
		}
		if err != nil {
			return err
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "roadmap.node_definition.updated", definitionID, map[string]any{"definition_id": definitionID, "definition_version": item.Version})
		return err
	})
	return &item, err
}

func (db *Database) ArchiveSpaceRoadmapNodeDefinition(ctx context.Context, userID, spaceID, definitionID string, expected int64) error {
	if expected < 1 {
		return ErrSpaceInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionTasksManage); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE space_roadmap_node_definitions SET archived_at=NOW(),version=version+1,updated_at=NOW() WHERE id=$1 AND space_id=$2 AND archived_at IS NULL AND version=$3`, definitionID, spaceID, expected)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return ErrSpaceConflict
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "roadmap.node_definition.archived", definitionID, map[string]any{"definition_id": definitionID})
		return err
	})
}

func validateSpaceRoadmapNodeTx(ctx context.Context, tx *sql.Tx, spaceID, roadmapID string, item *SpaceRoadmapNode, allowArchivedDefinition bool) error {
	item.NodeKind, item.Title, item.Description = strings.TrimSpace(item.NodeKind), strings.TrimSpace(item.Title), strings.TrimSpace(item.Description)
	if !validRoadmapNodeKind(item.NodeKind) || !validRoadmapText(item.Title, 240) || len([]rune(item.Description)) > 20000 || !validRoadmapPosition(item.PositionX) || !validRoadmapPosition(item.PositionY) {
		return ErrSpaceInvalid
	}
	if len(item.FieldValues) == 0 {
		item.FieldValues = json.RawMessage(`{}`)
	}
	if item.MilestoneID != "" {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_roadmap_milestones WHERE id=$1 AND roadmap_id=$2 AND space_id=$3 AND archived_at IS NULL)`, item.MilestoneID, roadmapID, spaceID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrSpaceNotFound
		}
	}
	if item.NodeKind == "custom" {
		if item.DefinitionID == "" {
			return ErrSpaceInvalid
		}
		var raw json.RawMessage
		definitionQuery := `SELECT field_schema FROM space_roadmap_node_definitions WHERE id=$1 AND space_id=$2`
		if !allowArchivedDefinition {
			definitionQuery += ` AND archived_at IS NULL`
		}
		if err := tx.QueryRowContext(ctx, definitionQuery, item.DefinitionID, spaceID).Scan(&raw); errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceNotFound
		} else if err != nil {
			return err
		}
		fields, ok := decodeRoadmapFieldSchema(raw)
		if !ok || !validateRoadmapFieldValues(item.FieldValues, fields) {
			return ErrSpaceInvalid
		}
	} else if item.DefinitionID != "" || len(item.FieldValues) > 65536 || !json.Valid(item.FieldValues) {
		return ErrSpaceInvalid
	}
	return nil
}

func (db *Database) CreateSpaceRoadmapNode(ctx context.Context, userID, spaceID, roadmapID string, item SpaceRoadmapNode, expected int64) (*SpaceRoadmapNode, int64, error) {
	item.ID, item.SpaceID, item.RoadmapID = "roadmap_node_"+uuid.NewString(), spaceID, roadmapID
	version, err := roadmapMutationTx(ctx, db, userID, spaceID, roadmapID, expected, func(tx *sql.Tx, graphVersion int64) error {
		if err := validateSpaceRoadmapNodeTx(ctx, tx, spaceID, roadmapID, &item, false); err != nil {
			return err
		}
		if err := scanSpaceRoadmapNode(tx.QueryRowContext(ctx, `INSERT INTO space_roadmap_nodes(id,space_id,roadmap_id,milestone_id,definition_id,node_kind,title,description,target_date,position_x,position_y,field_values) VALUES($1,$2,$3,NULLIF($4,''),NULLIF($5,''),$6,$7,$8,$9,$10,$11,$12) RETURNING `+roadmapNodeColumns, item.ID, spaceID, roadmapID, item.MilestoneID, item.DefinitionID, item.NodeKind, item.Title, item.Description, item.TargetDate, item.PositionX, item.PositionY, item.FieldValues), &item); err != nil {
			return err
		}
		_, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "roadmap.node.created", item.ID, map[string]any{"roadmap_id": roadmapID, "graph_version": graphVersion, "node_kind": item.NodeKind})
		return err
	})
	return &item, version, err
}

func (db *Database) UpdateSpaceRoadmapNode(ctx context.Context, userID, spaceID, roadmapID, nodeID string, item SpaceRoadmapNode, expected int64) (*SpaceRoadmapNode, int64, error) {
	version, err := roadmapMutationTx(ctx, db, userID, spaceID, roadmapID, expected, func(tx *sql.Tx, graphVersion int64) error {
		var existingKind, existingDefinition string
		if err := tx.QueryRowContext(ctx, `SELECT node_kind,COALESCE(definition_id,'') FROM space_roadmap_nodes WHERE id=$1 AND roadmap_id=$2 AND space_id=$3 AND archived_at IS NULL`, nodeID, roadmapID, spaceID).Scan(&existingKind, &existingDefinition); errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceNotFound
		} else if err != nil {
			return err
		}
		if item.NodeKind != existingKind || item.DefinitionID != existingDefinition {
			return ErrSpaceInvalid
		}
		if err := validateSpaceRoadmapNodeTx(ctx, tx, spaceID, roadmapID, &item, true); err != nil {
			return err
		}
		err := scanSpaceRoadmapNode(tx.QueryRowContext(ctx, `UPDATE space_roadmap_nodes SET milestone_id=NULLIF($1,''),title=$2,description=$3,target_date=$4,position_x=$5,position_y=$6,field_values=$7,version=version+1,updated_at=NOW() WHERE id=$8 AND roadmap_id=$9 AND space_id=$10 AND archived_at IS NULL RETURNING `+roadmapNodeColumns, item.MilestoneID, item.Title, item.Description, item.TargetDate, item.PositionX, item.PositionY, item.FieldValues, nodeID, roadmapID, spaceID), &item)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceNotFound
		}
		if err != nil {
			return err
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "roadmap.node.updated", nodeID, map[string]any{"roadmap_id": roadmapID, "graph_version": graphVersion, "node_kind": item.NodeKind})
		return err
	})
	return &item, version, err
}

func (db *Database) ArchiveSpaceRoadmapNode(ctx context.Context, userID, spaceID, roadmapID, nodeID string, expected int64) (int64, error) {
	return roadmapMutationTx(ctx, db, userID, spaceID, roadmapID, expected, func(tx *sql.Tx, graphVersion int64) error {
		result, err := tx.ExecContext(ctx, `UPDATE space_roadmap_nodes SET archived_at=NOW(),version=version+1,updated_at=NOW() WHERE id=$1 AND roadmap_id=$2 AND space_id=$3 AND archived_at IS NULL`, nodeID, roadmapID, spaceID)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return ErrSpaceNotFound
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM space_roadmap_edges WHERE roadmap_id=$1 AND ((source_kind='node' AND source_id=$2) OR (target_kind='node' AND target_id=$2))`, roadmapID, nodeID); err != nil {
			return err
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "roadmap.node.archived", nodeID, map[string]any{"roadmap_id": roadmapID, "graph_version": graphVersion})
		return err
	})
}

func roadmapDefinitionAgendaVisible(kind string) bool {
	return kind == "risk" || kind == "decision" || kind == "metric"
}

func roadmapNodeDateRange(date time.Time) (time.Time, time.Time) {
	return date, date.Add(24 * time.Hour)
}

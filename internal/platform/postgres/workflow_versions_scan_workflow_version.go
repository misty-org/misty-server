package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

func scanWorkflowVersion(scanner interface{ Scan(...any) error }, out *WorkflowVersion) error {
	var metadataRaw []byte
	if err := scanner.Scan(&out.ID, &out.WorkflowID, &out.SpaceID, &out.StableIdentifier, &out.Version, &out.Name, &out.Description, &out.AuthorName, &metadataRaw, &out.Definition, &out.ChecksumSHA256, &out.CreatedByUserID, &out.CreatedAt); err != nil {
		return err
	}
	if err := json.Unmarshal(metadataRaw, &out.Metadata); err != nil {
		return fmt.Errorf("decode workflow metadata: %w", err)
	}
	return nil
}

func validJSONObject(raw json.RawMessage) bool {
	if len(raw) == 0 || !json.Valid(raw) {
		return false
	}
	var value map[string]any
	return json.Unmarshal(raw, &value) == nil && value != nil
}

func TestingValidateCapabilityInput(capability WorkflowCapability, raw json.RawMessage) error {
	var input map[string]any
	if json.Unmarshal(raw, &input) != nil || input == nil {
		return ErrSpaceInvalid
	}
	for _, field := range capability.Inputs {
		value, exists := input[field.Name]
		if !exists || value == nil {
			if field.Required {
				return ErrSpaceInvalid
			}
			continue
		}
		if !workflowValueMatchesType(value, field.Type) {
			return ErrSpaceInvalid
		}
		if field.Required && strings.EqualFold(strings.TrimSpace(field.Type), "string") && strings.TrimSpace(value.(string)) == "" {
			return ErrSpaceInvalid
		}
	}
	return nil
}

func TestingValidateWorkflowVersionDefinition(metadata WorkflowMetadata, definition json.RawMessage) error {
	var parsed workflowv2.Definition
	if json.Unmarshal(definition, &parsed) != nil || len(parsed.Dependencies) > 0 {
		return ErrSpaceInvalid
	}
	if err := workflowv2.Validate(parsed, workflowv2.CoreRegistry(), nil); err != nil {
		return ErrSpaceInvalid
	}
	return nil
}

type workflowDependencyRecord struct {
	workflowID string
	checksum   string
	definition workflowv2.Definition
}

type workflowDependencyResolver map[string]workflowDependencyRecord

func (resolver workflowDependencyResolver) ResolveWorkflowVersion(versionID string) (string, string, workflowv2.Definition, bool) {
	item, ok := resolver[versionID]
	return item.workflowID, item.checksum, item.definition, ok
}

func validateWorkflowV2Tx(ctx context.Context, tx *sql.Tx, spaceID string, raw json.RawMessage) error {
	var root workflowv2.Definition
	if json.Unmarshal(raw, &root) != nil {
		return ErrSpaceInvalid
	}
	resolver := workflowDependencyResolver{}
	var load func(workflowv2.Definition) error
	load = func(definition workflowv2.Definition) error {
		for _, dependency := range definition.Dependencies {
			if _, loaded := resolver[dependency.VersionID]; loaded {
				continue
			}
			var workflowID, checksum string
			var childRaw []byte
			if err := tx.QueryRowContext(ctx, `SELECT workflow_id,checksum_sha256,definition FROM space_workflow_versions WHERE id=$1 AND space_id=$2`, dependency.VersionID, spaceID).Scan(&workflowID, &checksum, &childRaw); err != nil {
				return ErrSpaceInvalid
			}
			var child workflowv2.Definition
			if json.Unmarshal(childRaw, &child) != nil {
				return ErrSpaceInvalid
			}
			resolver[dependency.VersionID] = workflowDependencyRecord{workflowID: workflowID, checksum: checksum, definition: child}
			if err := load(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := load(root); err != nil {
		return err
	}
	if err := workflowv2.Validate(root, workflowv2.CoreRegistry(), resolver); err != nil {
		return ErrSpaceInvalid
	}
	return nil
}

func workflowValueMatchesType(value any, declaredType string) bool {
	switch strings.ToLower(strings.TrimSpace(declaredType)) {
	case "any", "json":
		return true
	case "string", "text":
		_, ok := value.(string)
		return ok
	case "boolean", "bool":
		_, ok := value.(bool)
		return ok
	case "number", "float", "double":
		_, ok := value.(float64)
		return ok
	case "integer", "int":
		number, ok := value.(float64)
		return ok && number == float64(int64(number))
	case "object", "map":
		_, ok := value.(map[string]any)
		return ok
	case "array", "list":
		_, ok := value.([]any)
		return ok
	case "null":
		return value == nil
	default:
		// Portable packages may define richer domain types. Their field-level
		// schema remains authoritative to the compatible workflow runtime.
		return true
	}
}

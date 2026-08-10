package db

import "encoding/json"

func TestingDecodeRoadmapFieldSchema(raw json.RawMessage) ([]SpaceRoadmapFieldDefinition, bool) {
	return decodeRoadmapFieldSchema(raw)
}

func TestingValidateRoadmapFieldValues(raw json.RawMessage, fields []SpaceRoadmapFieldDefinition) bool {
	return validateRoadmapFieldValues(raw, fields)
}

func TestingValidRoadmapDefinitionUpdate(existing, next []SpaceRoadmapFieldDefinition) bool {
	return validRoadmapDefinitionUpdate(existing, next)
}

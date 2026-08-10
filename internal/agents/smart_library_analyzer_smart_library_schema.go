package agent

func smartLibrarySchema() map[string]any {
	properties := map[string]any{
		"assetId":        map[string]any{"type": "string"},
		"contentType":    map[string]any{"type": "string", "minLength": 3, "maxLength": 80},
		"primarySubject": map[string]any{"type": "string", "minLength": 3, "maxLength": 160},
		"description":    map[string]any{"type": "string", "minLength": 24, "maxLength": 640},
		"tags":           stringArraySchema(5, 24), "searchTerms": stringArraySchema(5, 24),
		"entities": stringArraySchema(0, 16), "characters": stringArraySchema(0, 12),
		"brands": stringArraySchema(0, 12), "applications": stringArraySchema(0, 12),
		"objects": stringArraySchema(0, 20), "scenes": stringArraySchema(0, 12),
		"activities": stringArraySchema(0, 12), "colors": stringArraySchema(0, 12),
		"visibleText": stringArraySchema(0, 20), "topics": stringArraySchema(0, 16),
		"suggestedCollections": stringArraySchema(0, 8),
		"confidence":           map[string]any{"type": "number", "minimum": 0, "maximum": 1},
	}
	required := []string{"assetId", "contentType", "primarySubject", "description", "tags", "searchTerms", "entities", "characters", "brands", "applications", "objects", "scenes", "activities", "colors", "visibleText", "topics", "suggestedCollections", "confidence"}
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"assets"}, "properties": map[string]any{"assets": map[string]any{"type": "array", "maxItems": 8, "items": map[string]any{"type": "object", "additionalProperties": false, "required": required, "properties": properties}}}}
}

package api

import (
	"context"
	"slices"
)

// Agent preferences from Settings › Agents. They belong to the account, so a
// tool reads them on the server for every run on every device. A missing
// record or value means the setting's default.
const (
	prefWebSearch       = "agents.web_search"
	prefWebSearchEngine = "agents.web_search_engine"
	prefOfferSearchApps = "agents.offer_search_apps"
)

func (s *SpacesService) agentPreferences(ctx context.Context, userID string) map[string]any {
	if s == nil || s.database == nil || userID == "" {
		return nil
	}
	preferences, err := s.database.AccountPreferences(ctx, userID)
	if err != nil {
		return nil
	}
	return preferences.Values
}

func preferenceBool(values map[string]any, id string, fallback bool) bool {
	if value, ok := values[id].(bool); ok {
		return value
	}
	return fallback
}

func preferenceChoice(values map[string]any, id, fallback string, choices ...string) string {
	if value, ok := values[id].(string); ok && slices.Contains(choices, value) {
		return value
	}
	return fallback
}

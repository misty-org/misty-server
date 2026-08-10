package api

import "encoding/json"

func TestingWorkflowEventIdentity(raw json.RawMessage) (string, string) {
	var item map[string]any
	if json.Unmarshal(raw, &item) != nil {
		return "", ""
	}
	provider, _ := item["provider"].(string)
	eventID, _ := item["eventId"].(string)
	if eventID == "" {
		eventID, _ = item["event_id"].(string)
	}
	return provider, eventID
}

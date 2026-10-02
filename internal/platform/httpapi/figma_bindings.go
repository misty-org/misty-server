package api

type figmaBindingInput struct {
	ConnectionID string `json:"connection_id"`
	ResourceType string `json:"resource_type"`
	TeamID       string `json:"team_id"`
	ProjectID    string `json:"project_id"`
	FileKey      string `json:"file_key"`
	FileURL      string `json:"file_url"`
}

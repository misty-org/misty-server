package api

type mistyConversationMessage struct {
	ID          string                        `json:"id"`
	Role        string                        `json:"role"`
	Mode        string                        `json:"mode"`
	Content     string                        `json:"content"`
	CreatedAt   string                        `json:"createdAt"`
	State       string                        `json:"state"`
	Retryable   bool                          `json:"retryable,omitempty"`
	Action      *mistyConversationAction      `json:"action,omitempty"`
	Attachments []mistyConversationAttachment `json:"attachments,omitempty"`
}

type mistyConversationAttachment struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	MIMEType   string `json:"mimeType"`
	ByteSize   int64  `json:"byteSize"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	PreviewURL string `json:"previewUrl"`
	State      string `json:"state"`
}

type mistyConversationAction struct {
	ID                   string `json:"id"`
	Title                string `json:"title"`
	Summary              string `json:"summary"`
	Prompt               string `json:"prompt"`
	Risk                 string `json:"risk"`
	State                string `json:"state"`
	RequiresConfirmation bool   `json:"requiresConfirmation"`
	RunID                string `json:"runId"`
	ResultHref           string `json:"resultHref"`
	Error                string `json:"error,omitempty"`
}

type mistyConversation struct {
	ID            string                     `json:"id"`
	Title         string                     `json:"title"`
	AgentID       string                     `json:"agentId,omitempty"`
	SpaceID       string                     `json:"spaceId,omitempty"`
	Kind          string                     `json:"kind"`
	OriginSurface string                     `json:"originSurface,omitempty"`
	OriginHref    string                     `json:"originHref,omitempty"`
	Privacy       string                     `json:"privacyBoundary,omitempty"`
	ModelID       string                     `json:"modelId"`
	Reasoning     string                     `json:"reasoningEffort,omitempty"`
	CreatedAt     string                     `json:"createdAt"`
	UpdatedAt     string                     `json:"updatedAt"`
	Messages      []mistyConversationMessage `json:"messages"`
	Remote        bool                       `json:"remote"`
}

type mistyContextReference struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	Href      string `json:"href,omitempty"`
	SpaceID   string `json:"spaceId,omitempty"`
	SpaceName string `json:"spaceName,omitempty"`
	Attached  bool   `json:"attached,omitempty"`
}

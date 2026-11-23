package transport

type AccountEvent struct {
	UserID string `json:"userId,omitempty"`
	Topic  string `json:"topic"`
	ID     string `json:"id,omitempty"`
}

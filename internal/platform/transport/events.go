package transport

// AccountEvent is the shared invalidation hint carried by the account event bus.
type AccountEvent struct {
	UserID string `json:"userId,omitempty"`
	Topic  string `json:"topic"`
	ID     string `json:"id,omitempty"`
}

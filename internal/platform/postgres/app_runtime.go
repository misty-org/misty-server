package db

import (
	"errors"
	"time"
)

var ErrAppRuntimeForbidden = errors.New("retired app authority is forbidden")

type AppRuntimeSession struct {
	AuthorityGeneration int64     `json:"-"`
	UserID              string    `json:"-"`
	AppID               string    `json:"app_id"`
	SpaceID             string    `json:"space_id,omitempty"`
	Scopes              []string  `json:"scopes"`
	ExpiresAt           time.Time `json:"expires_at"`
}

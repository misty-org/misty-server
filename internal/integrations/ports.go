// Package integrations owns OAuth state, provider credentials, callbacks,
// events, cloud connections, and provider-specific resource access.
package integrations

import (
	"context"
	"time"
)

type Credential struct {
	Provider  string
	Token     string
	ExpiresAt time.Time
}

// Credentials stores encrypted provider credentials.
type Credentials interface {
	Load(context.Context, string, string) (Credential, error)
	Save(context.Context, string, Credential) error
	Delete(context.Context, string, string) error
}

// Provider performs a narrowly scoped provider operation.
type Provider interface {
	AuthorizeURL(context.Context, string) (string, error)
	Exchange(context.Context, string) (Credential, error)
}

// Package accounts owns identities, authentication, sessions, profile
// settings, password recovery, exports, and account lifecycle policy.
package accounts

import (
	"context"
	"time"
)

type UserID string

type User struct {
	ID        UserID
	Email     string
	Name      string
	CreatedAt time.Time
}

// Repository is the persistence required by account use cases.
type Repository interface {
	UserByID(context.Context, UserID) (User, error)
	UserBySession(context.Context, string) (User, error)
	SaveProfile(context.Context, User) error
	RevokeSessions(context.Context, UserID) error
}

// PasswordResetSender delivers a recovery link without exposing the delivery
// provider to account policy.
type PasswordResetSender interface {
	SendPasswordResetEmail(context.Context, string, string) error
}

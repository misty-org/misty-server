// Package spaces owns collaboration membership, invitations, conversations,
// tasks, calendars, templates, inboxes, nodes, and realtime authorization.
package spaces

import "context"

type SpaceID string
type UserID string

type Membership struct {
	SpaceID SpaceID
	UserID  UserID
	Role    string
}

// Repository is the persistence required by Space collaboration policy.
type Repository interface {
	Membership(context.Context, SpaceID, UserID) (Membership, error)
	ListMembers(context.Context, SpaceID) ([]Membership, error)
}

// Events publishes committed collaboration changes.
type Events interface {
	PublishSpaceEvent(context.Context, SpaceID, string, any) error
}

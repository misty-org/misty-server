// Package journal owns notes, drawings, collaboration tickets, assets, and
// the command queues shared with the Cloudflare collaboration worker.
package journal

import (
	"context"
	"time"
)

type RoomID string

type Ticket struct {
	Value     string
	ExpiresAt time.Time
}

// TicketIssuer creates short-lived credentials for a collaboration room.
type TicketIssuer interface {
	IssueTicket(context.Context, RoomID, string) (Ticket, error)
}

// CommandQueue persists server-to-worker control commands.
type CommandQueue interface {
	Enqueue(context.Context, RoomID, string, []byte) error
}

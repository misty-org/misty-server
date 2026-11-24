package admit

import (
	"context"
	"errors"
	"strings"

	"github.com/misty-org/misty-server/internal/collab/wire"
)

const (
	RoleEdit = "edit"
	RoleView = "view"

	KindPerson      = "person"
	KindIntegration = "integration"

	AccessRoomsRead  = "rooms.read"
	AccessRoomsWrite = "rooms.write"
)

var (
	ErrUnauthorized = errors.New("admit: the token is not recognized")
	ErrNotMember    = errors.New("admit: not a member of the Space")
	ErrSpaceMissing = errors.New("admit: the Space does not exist")
	ErrFileMissing  = errors.New("admit: the file does not exist")
)

type PersonToken struct {
	UserID    string
	SessionID string
}

type IntegrationToken struct {
	ID        string
	SpaceID   string
	Access    []string
	KeyPrefix string
	Name      string
}

type Member struct {
	Name   string
	Avatar string
	Tone   string
}

type FileAccess struct {
	Read  bool
	Write bool
}

type Gate struct {
	VerifyPerson      func(token string) (PersonToken, error)
	VerifyIntegration func(token string) (IntegrationToken, error)
	Member            func(ctx context.Context, spaceID, userID string) (Member, error)
	SpaceExists       func(ctx context.Context, spaceID string) (bool, error)
	File              func(ctx context.Context, spaceID, itemID, userID string) (FileAccess, error)
}

type Who struct {
	Kind        string
	ID          string
	SessionID   string
	Integration IntegrationToken
	Identity    wire.Identity
}

type Grant struct {
	Who  Who
	Role string
}

type Refusal struct {
	Code   int
	Reason string
}

func (r *Refusal) Error() string { return wire.CloseName(r.Code) + ": " + r.Reason }

func Refuse(code int, reason string) error { return &Refusal{Code: code, Reason: reason} }

func CodeOf(err error) int {
	var refusal *Refusal
	if errors.As(err, &refusal) {
		return refusal.Code
	}
	return wire.CloseInternal
}

func (g Gate) Authenticate(token string) (Who, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return Who{}, Refuse(wire.CloseUnauthorized, "a token is required")
	}
	if g.VerifyPerson != nil {
		if person, err := g.VerifyPerson(token); err == nil && person.UserID != "" {
			return Who{Kind: KindPerson, ID: person.UserID, SessionID: person.SessionID,
				Identity: wire.Identity{Kind: KindPerson, ID: person.UserID}}, nil
		}
	}
	if g.VerifyIntegration != nil {
		if integration, err := g.VerifyIntegration(token); err == nil && integration.ID != "" {
			return Who{Kind: KindIntegration, ID: integration.ID, Integration: integration,
				Identity: wire.Identity{Kind: KindIntegration, ID: integration.ID, Name: integration.Name}}, nil
		}
	}
	return Who{}, Refuse(wire.CloseUnauthorized, "the token is missing, expired or not recognized")
}

func (g Gate) Check(ctx context.Context, who Who, spaceID, key, fileID string) (Grant, error) {
	grant := Grant{Who: who, Role: RoleEdit}
	switch who.Kind {
	case KindPerson:
		member, err := g.Member(ctx, spaceID, who.ID)
		if err != nil {
			return Grant{}, spaceRefusal(err)
		}
		grant.Who.Identity.Name, grant.Who.Identity.Avatar, grant.Who.Identity.Tone = member.Name, member.Avatar, member.Tone
	case KindIntegration:
		integration := who.Integration
		if fileID != "" {
			return Grant{}, Refuse(wire.CloseForbidden, "an integration has rooms only, not the drive")
		}
		if integration.SpaceID != spaceID || !strings.HasPrefix(key, integration.KeyPrefix) {
			return Grant{}, Refuse(wire.CloseForbidden, "this integration has no access to this room")
		}
		reads, writes := false, false
		for _, access := range integration.Access {
			reads = reads || access == AccessRoomsRead
			writes = writes || access == AccessRoomsWrite
		}
		if !reads && !writes {
			return Grant{}, Refuse(wire.CloseForbidden, "this integration has no room access")
		}
		if g.SpaceExists != nil {
			exists, err := g.SpaceExists(ctx, spaceID)
			if err != nil {
				return Grant{}, err
			}
			if !exists {
				return Grant{}, Refuse(wire.CloseNotFound, "the Space does not exist")
			}
		}
		if !writes {
			grant.Role = RoleView
		}
	default:
		return Grant{}, Refuse(wire.CloseUnauthorized, "the token is not recognized")
	}
	if fileID == "" {
		return grant, nil
	}
	userID := ""
	if who.Kind == KindPerson {
		userID = who.ID
	}
	access, err := g.File(ctx, spaceID, fileID, userID)
	if err != nil {
		return Grant{}, spaceRefusal(err)
	}
	if !access.Read {
		return Grant{}, Refuse(wire.CloseForbidden, "no access to this file")
	}
	if !access.Write {
		grant.Role = RoleView
	}
	return grant, nil
}

func spaceRefusal(err error) error {
	switch {
	case errors.Is(err, ErrNotMember):
		return Refuse(wire.CloseForbidden, "not a member of this Space")
	case errors.Is(err, ErrSpaceMissing):
		return Refuse(wire.CloseNotFound, "the Space does not exist")
	case errors.Is(err, ErrFileMissing):
		return Refuse(wire.CloseNotFound, "the file does not exist")
	}
	return err
}

func Lower(before, after string) bool {
	return before == RoleEdit && after == RoleView
}

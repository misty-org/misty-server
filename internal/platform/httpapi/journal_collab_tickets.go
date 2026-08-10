package api

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

func (c JournalCollabConfig) mintResourceTicketWithLifetime(
	userID, spaceID string,
	resource collaborationResource,
	role string,
	aclVersion int64,
	lifetime time.Duration,
) (JournalTicket, error) {
	if userID == "" || spaceID == "" || resource.ID == "" ||
		resource.Type == "" || resource.Party == "" || role == "" || aclVersion < 1 {
		return JournalTicket{}, errors.New("incomplete ticket claims")
	}
	room := c.resourceRoomID(resource.Type, resource.ID)
	if lifetime < journalTicketLifetime || lifetime > 15*time.Minute {
		return JournalTicket{}, errors.New("invalid ticket lifetime")
	}
	expiresAt := time.Now().Add(lifetime).UTC()
	claims := TestingJournalTicketClaims{
		Issuer: c.Issuer, Audience: c.Audience, JTI: "tkt_" + uuid.NewString(),
		Subject: userID, SpaceID: spaceID,
		ResourceType: resource.Type, ResourceID: resource.ID, Room: room,
		Role: role, ACLVersion: aclVersion, Expires: expiresAt.Unix(),
	}
	if resource.Type == "note" {
		claims.NoteID = resource.ID
	} else if resource.Type == "drawing" {
		claims.DrawingID = resource.ID
	}
	header, err := json.Marshal(map[string]string{"alg": "EdDSA", "typ": "JWT"})
	if err != nil {
		return JournalTicket{}, err
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return JournalTicket{}, err
	}
	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." +
		base64.RawURLEncoding.EncodeToString(payload)
	signature := ed25519.Sign(c.TestingPrivateKey, []byte(signingInput))
	return JournalTicket{
		Ticket: signingInput + "." + base64.RawURLEncoding.EncodeToString(signature),
		Room:   room,
		// The party segment is the kebab-cased Durable Object binding name.
		URL:       fmt.Sprintf("wss://%s/parties/%s/%s", c.Host, resource.Party, room),
		Role:      role,
		ExpiresAt: expiresAt,
	}, nil
}

// MintJournalExportTicket creates a longer-lived, single-use viewer ticket for
// the account export client. It can read exactly one raw Yjs update and cannot
// submit edits.
func (c JournalCollabConfig) MintJournalExportTicket(
	userID, spaceID, resourceType, resourceID string, aclVersion int64,
) (JournalTicket, error) {
	party := "note-room"
	if resourceType == "drawing" {
		party = "drawing-room"
	} else if resourceType != "note" {
		return JournalTicket{}, errors.New("invalid export resource type")
	}
	return c.mintResourceTicketWithLifetime(
		userID,
		spaceID,
		collaborationResource{
			Type: resourceType, ID: resourceID, Party: party,
		},
		"viewer",
		aclVersion,
		15*time.Minute,
	)
}

// SetJournalCollab installs the shared Journal collaboration configuration.
func (s *SpacesService) SetJournalCollab(config JournalCollabConfig) {
	s.TestingJournalCollab = config
}

// JournalCollab exposes the configuration for control commands and callbacks.
func (s *SpacesService) JournalCollab() JournalCollabConfig { return s.TestingJournalCollab }

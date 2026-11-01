package api

import (
	"context"
	"errors"
	"fmt"

	"github.com/kannachi323/misty/server/internal/integrations/composio"
)

// The parts of an account's purge outside the database (docs/cloud/README.md):
// its connected apps at Composio, its agent chat attachments in R2, and the
// Spaces it owned, whose Library objects go with the Space purge.

// AppConnectionRemover lists and deletes one account's connected apps.
// Production uses Composio; tests use a fake.
type AppConnectionRemover interface {
	Accounts(ctx context.Context, userID, toolkit string) ([]composio.Account, error)
	DeleteAccount(ctx context.Context, userID, id string) error
}

// TestingSetAppConnections replaces Composio for the account purge.
func (s *SpacesService) TestingSetAppConnections(remover AppConnectionRemover) {
	s.appConnections = remover
}

func (s *SpacesService) appConnectionRemover() (AppConnectionRemover, error) {
	if s.appConnections != nil {
		return s.appConnections, nil
	}
	return composioClient()
}

// removeAppConnections deletes every app the account connected through
// Composio. A server without Composio (development) has none to delete.
func (s *SpacesService) removeAppConnections(ctx context.Context, userID string, status map[string]string) error {
	remover, err := s.appConnectionRemover()
	if errors.Is(err, composio.ErrUnavailable) {
		status["apps"] = "not_configured"
		return nil
	}
	if err != nil {
		return err
	}
	owner := composio.UserID(userID)
	accounts, err := remover.Accounts(ctx, owner, "")
	if err != nil {
		status["apps"] = "list_failed"
		return fmt.Errorf("list connected apps: %w", err)
	}
	for _, account := range accounts {
		if err := remover.DeleteAccount(ctx, owner, account.ID); err != nil && !composio.NotFound(err) {
			status["apps"] = "delete_failed"
			return fmt.Errorf("delete connected app: %w", err)
		}
	}
	status["apps"] = fmt.Sprintf("deleted_%d", len(accounts))
	return nil
}

// deleteStoredObjects removes keys from the shared object store; ones already
// gone are fine.
func (s *SpacesService) deleteStoredObjects(ctx context.Context, keys []string) error {
	if s.avatarStore == nil {
		return nil
	}
	for _, key := range keys {
		if key == "" {
			continue
		}
		if err := s.avatarStore.Delete(ctx, key); err != nil && !errors.Is(err, ErrLibraryObjectNotFound) {
			return fmt.Errorf("delete stored object: %w", err)
		}
	}
	return nil
}

// PurgeDeletedSpaces removes deleted Spaces whose recovery window ended: an
// owner's deleted Space after 30 days, a deleted account's Spaces at once.
// A Space waits until its notes' and drawings' rooms confirm their purge.
func (s *SpacesService) PurgeDeletedSpaces(ctx context.Context, limit int) (int, error) {
	ids, err := s.database.DueSpacePurges(ctx, limit)
	if err != nil {
		return 0, err
	}
	purged := 0
	for _, id := range ids {
		waiting, err := s.database.BeginSpacePurge(ctx, id)
		if err != nil {
			return purged, err
		}
		if waiting > 0 {
			continue
		}
		keys, err := s.database.SpacePurgeObjectKeys(ctx, id)
		if err != nil {
			return purged, err
		}
		if err := s.deleteStoredObjects(ctx, keys); err != nil {
			return purged, err
		}
		if err := s.database.DeleteSpaceData(ctx, id); err != nil {
			return purged, err
		}
		purged++
	}
	return purged, nil
}

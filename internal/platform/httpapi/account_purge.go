package api

import (
	"context"
	"errors"
	"fmt"
)

// The parts of an account's purge outside the database:
// the Spaces it owned, whose Library objects go with the Space purge.

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

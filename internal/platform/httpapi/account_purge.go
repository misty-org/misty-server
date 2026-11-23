package api

import (
	"context"
	"errors"
	"fmt"
)

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

package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// LibraryReconciliationReport contains counts only. Object keys, filenames,
// user IDs, and checksums are intentionally excluded so this can be logged and
// exported to monitoring without leaking user data.
type LibraryReconciliationReport struct {
	ExpiredUploads           int
	InventoryObjectsChecked  int
	OrphanObjectsDeleted     int
	MissingPermanentObjects  int
	MismatchedObjects        int
	InterruptedFinalizations int
}

// ReconcileLibraryObjects heals safe object-store drift and reports drift that
// needs restoration or a normal client retry. It never downloads an object
// body and never guesses how to finalize an upload.
func (s *SpaceLibraryService) ReconcileLibraryObjects(
	ctx context.Context, orphanGrace time.Duration, limit int,
) (LibraryReconciliationReport, error) {
	report := LibraryReconciliationReport{}
	if s == nil || s.database == nil || s.TestingStore == nil {
		return report, errors.New("Library reconciliation is not configured")
	}
	if orphanGrace < time.Hour {
		orphanGrace = 2 * time.Hour
	}
	if limit < 1 || limit > 1000 {
		limit = 250
	}

	expired, err := s.CleanupExpired(ctx, limit)
	if err != nil {
		return report, fmt.Errorf("expire upload reservations: %w", err)
	}
	report.ExpiredUploads = expired

	if inventory, ok := s.TestingStore.(LibraryObjectInventory); ok {
		if err := s.reconcileInventoryPage(
			ctx, inventory, orphanGrace, limit, &report,
		); err != nil {
			return report, err
		}
	}

	ready, err := s.database.LibraryReadyBlobExpectations(ctx, limit)
	if err != nil {
		return report, fmt.Errorf("load permanent object expectations: %w", err)
	}
	for _, expected := range ready {
		metadata, headErr := s.TestingStore.Head(ctx, expected.ObjectKey)
		if errors.Is(headErr, ErrLibraryObjectNotFound) {
			report.MissingPermanentObjects++
			continue
		}
		if headErr != nil {
			return report, fmt.Errorf("verify permanent object: %w", headErr)
		}
		if !TestingObjectMatchesExpectation(metadata, expected) {
			report.MismatchedObjects++
		}
	}

	pending, err := s.database.LibraryInterruptedFinalizations(ctx, limit)
	if err != nil {
		return report, fmt.Errorf("load interrupted finalizations: %w", err)
	}
	for _, expected := range pending {
		metadata, headErr := s.TestingStore.Head(ctx, expected.ObjectKey)
		if errors.Is(headErr, ErrLibraryObjectNotFound) {
			continue
		}
		if headErr != nil {
			return report, fmt.Errorf("verify interrupted finalization: %w", headErr)
		}
		if TestingObjectMatchesExpectation(metadata, expected) {
			report.InterruptedFinalizations++
		} else {
			report.MismatchedObjects++
		}
	}
	return report, nil
}

func (s *SpaceLibraryService) reconcileInventoryPage(
	ctx context.Context,
	inventory LibraryObjectInventory,
	orphanGrace time.Duration,
	limit int,
	report *LibraryReconciliationReport,
) error {
	s.reconciliationMu.Lock()
	defer s.reconciliationMu.Unlock()

	page, err := inventory.List(ctx, "library/", s.reconciliationCursor, limit)
	if err != nil {
		return fmt.Errorf("list object inventory: %w", err)
	}
	keys := make([]string, 0, len(page.Objects))
	for _, object := range page.Objects {
		keys = append(keys, object.Key)
	}
	expected, err := s.database.LibraryObjectExpectations(ctx, keys)
	if err != nil {
		return fmt.Errorf("load object inventory expectations: %w", err)
	}
	cutoff := time.Now().UTC().Add(-orphanGrace)
	for _, object := range page.Objects {
		report.InventoryObjectsChecked++
		expectation, known := expected[object.Key]
		if !known {
			if object.LastModified.IsZero() || object.LastModified.After(cutoff) {
				continue
			}
			if err := s.TestingStore.Delete(ctx, object.Key); err != nil &&
				!errors.Is(err, ErrLibraryObjectNotFound) {
				return fmt.Errorf("delete orphan object: %w", err)
			}
			report.OrphanObjectsDeleted++
			continue
		}
		if object.ByteSize != expectation.ByteSize {
			report.MismatchedObjects++
		}
	}
	s.reconciliationCursor = page.NextCursor
	return nil
}

func TestingObjectMatchesExpectation(
	metadata LibraryObjectMetadata, expected db.LibraryObjectExpectation,
) bool {
	return metadata.ByteSize == expected.ByteSize && metadata.SHA256 == expected.SHA256
}

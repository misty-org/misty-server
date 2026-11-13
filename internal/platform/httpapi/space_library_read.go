package api

import (
	"context"
	"io"

	"github.com/misty-org/misty-server/internal/platform/db"
)

// ReadLibraryItem returns one Library file's bytes, for someone who may open it.
func (s *SpaceLibraryService) ReadLibraryItem(ctx context.Context, userID, spaceID, itemID string, maximumBytes int64) ([]byte, *db.LibraryDownload, error) {
	if maximumBytes < 1 || maximumBytes > 25_000_000 {
		maximumBytes = 25_000_000
	}
	download, err := s.database.ExplicitLibraryItemDownload(ctx, userID, spaceID, itemID)
	if err != nil {
		return nil, nil, err
	}
	if download.ByteSize > maximumBytes {
		return nil, download, db.ErrLibraryInvalid
	}
	stream, metadata, err := s.Store.Open(ctx, download.ObjectKey)
	if err != nil {
		return nil, download, err
	}
	defer stream.Close()
	if metadata.ByteSize != download.ByteSize || metadata.SHA256 != download.SHA256 {
		return nil, download, db.ErrLibraryUploadMismatch
	}
	data, err := io.ReadAll(io.LimitReader(stream, maximumBytes+1))
	if err != nil || int64(len(data)) > maximumBytes {
		if err == nil {
			err = db.ErrLibraryInvalid
		}
		return nil, download, err
	}
	return data, download, nil
}

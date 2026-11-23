package api

import (
	"context"
	"errors"
	"io"
	"regexp"
	"time"
)

var (
	ErrLibraryObjectNotFound = errors.New("library object not found")
	libraryObjectKeyPattern  = regexp.MustCompile(`^(?:library|avatars)/[a-zA-Z0-9_-]{8,160}$`)
	sha256Pattern            = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

const LibrarySHA256MetadataKey = "misty-library-sha256"

type cancelOnCloseReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (reader *cancelOnCloseReadCloser) Close() error {
	err := reader.ReadCloser.Close()
	reader.cancel()
	return err
}

type LibraryObjectMetadata struct {
	ByteSize int64
	SHA256   string
	MIMEType string
}

type LibraryObjectStore interface {
	Health(context.Context) error
	Put(context.Context, string, io.Reader, LibraryObjectMetadata) error
	Head(context.Context, string) (LibraryObjectMetadata, error)
	Open(context.Context, string) (io.ReadCloser, LibraryObjectMetadata, error)
	Delete(context.Context, string) error
}

type LibraryObjectInventory interface {
	List(context.Context, string, string, int) (LibraryObjectPage, error)
}

type LibraryObjectPage struct {
	Objects    []LibraryObjectEntry
	NextCursor string
}

type LibraryObjectEntry struct {
	Key          string
	ByteSize     int64
	LastModified time.Time
}

type LibraryObjectUpload struct {
	URL       string            `json:"url"`
	Method    string            `json:"method"`
	Headers   map[string]string `json:"headers"`
	ExpiresAt time.Time         `json:"expires_at"`
}

func validateLibraryObject(key string, metadata LibraryObjectMetadata) error {
	if !libraryObjectKeyPattern.MatchString(key) || metadata.ByteSize < 1 || metadata.ByteSize > 1_000_000_000 || !sha256Pattern.MatchString(metadata.SHA256) {
		return errors.New("invalid Library object constraints")
	}
	return nil
}

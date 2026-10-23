package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

type memoryLibraryObject struct {
	data     []byte
	metadata LibraryObjectMetadata
	created  time.Time
}

type MemoryLibraryObjectStore struct {
	mu      sync.RWMutex
	objects map[string]memoryLibraryObject
}

func (s *MemoryLibraryObjectStore) Health(_ context.Context) error { return nil }

func NewMemoryLibraryObjectStore() *MemoryLibraryObjectStore {
	return &MemoryLibraryObjectStore{objects: map[string]memoryLibraryObject{}}
}

func (s *MemoryLibraryObjectStore) Put(_ context.Context, key string, body io.Reader, metadata LibraryObjectMetadata) error {
	if err := validateLibraryObject(key, metadata); err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(body, metadata.ByteSize+1))
	if err != nil {
		return err
	}
	actual := sha256.Sum256(data)
	if int64(len(data)) != metadata.ByteSize || hex.EncodeToString(actual[:]) != metadata.SHA256 {
		return errors.New("library object does not match upload constraints")
	}
	s.mu.Lock()
	s.objects[key] = memoryLibraryObject{
		data: append([]byte(nil), data...), metadata: metadata, created: time.Now().UTC(),
	}
	s.mu.Unlock()
	return nil
}

func (s *MemoryLibraryObjectStore) Head(_ context.Context, key string) (LibraryObjectMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	object, ok := s.objects[key]
	if !ok {
		return LibraryObjectMetadata{}, ErrLibraryObjectNotFound
	}
	return object.metadata, nil
}

func (s *MemoryLibraryObjectStore) Open(_ context.Context, key string) (io.ReadCloser, LibraryObjectMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	object, ok := s.objects[key]
	if !ok {
		return nil, LibraryObjectMetadata{}, ErrLibraryObjectNotFound
	}
	return io.NopCloser(bytes.NewReader(append([]byte(nil), object.data...))), object.metadata, nil
}

func (s *MemoryLibraryObjectStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	delete(s.objects, key)
	s.mu.Unlock()
	return nil
}

func (s *MemoryLibraryObjectStore) List(
	_ context.Context, prefix, cursor string, limit int,
) (LibraryObjectPage, error) {
	if limit < 1 || limit > 1000 {
		limit = 250
	}
	s.mu.RLock()
	keys := make([]string, 0, len(s.objects))
	for key := range s.objects {
		if strings.HasPrefix(key, prefix) && key > cursor {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if len(keys) > limit {
		keys = keys[:limit]
	}
	page := LibraryObjectPage{Objects: make([]LibraryObjectEntry, 0, len(keys))}
	for _, key := range keys {
		object := s.objects[key]
		page.Objects = append(page.Objects, LibraryObjectEntry{
			Key: key, ByteSize: object.metadata.ByteSize, LastModified: object.created,
		})
	}
	s.mu.RUnlock()
	if len(keys) == limit {
		page.NextCursor = keys[len(keys)-1]
	}
	return page, nil
}

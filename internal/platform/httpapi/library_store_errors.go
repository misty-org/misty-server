package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	ErrLibraryObjectNotFound = errors.New("library object not found")
	libraryObjectKeyPattern  = regexp.MustCompile(`^(?:library|avatars)/[a-zA-Z0-9_-]{8,160}$`)
	sha256Pattern            = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

const TestingLibrarySHA256MetadataKey = "misty-library-sha256"

func isLoopbackLibraryHost(host string) bool {
	return strings.EqualFold(host, "localhost") || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()
}

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

// LibraryObjectStore keeps permanent objects under the library/ and avatars/
// prefixes. The bucket is shared (Library items, Agent attachments, user
// avatars), so lifecycle rules must exclude these permanent prefixes.
type LibraryObjectStore interface {
	Health(context.Context) error
	Put(context.Context, string, io.Reader, LibraryObjectMetadata) error
	Head(context.Context, string) (LibraryObjectMetadata, error)
	Open(context.Context, string) (io.ReadCloser, LibraryObjectMetadata, error)
	Delete(context.Context, string) error
}

// LibraryObjectInventory is implemented by production object stores that can
// page through a prefix without opening object bodies. Reconciliation uses it
// to find old objects that are no longer referenced by PostgreSQL.
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

// LocalLibraryObjectStore is a persistent filesystem backend. Production
// configuration permits it only for explicitly self-hosted deployments.
type LocalLibraryObjectStore struct{ root string }

func (s *LocalLibraryObjectStore) Health(_ context.Context) error {
	info, err := os.Stat(s.root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("local Library store is not a directory")
	}
	return nil
}

func NewLocalLibraryObjectStore(root string) (*LocalLibraryObjectStore, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("local Library directory is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, err
	}
	return &LocalLibraryObjectStore{root: absolute}, nil
}

func (s *LocalLibraryObjectStore) paths(key string) (string, string, error) {
	if !libraryObjectKeyPattern.MatchString(key) {
		return "", "", ErrLibraryObjectNotFound
	}
	name := strings.TrimPrefix(key, "library/")
	return filepath.Join(s.root, name+".blob"), filepath.Join(s.root, name+".json"), nil
}

func (s *LocalLibraryObjectStore) Put(_ context.Context, key string, body io.Reader, metadata LibraryObjectMetadata) error {
	if err := validateLibraryObject(key, metadata); err != nil {
		return err
	}
	if err := ensureLocalLibraryCapacity(s.root, metadata.ByteSize); err != nil {
		return err
	}
	objectPath, metadataPath, _ := s.paths(key)
	if err := os.MkdirAll(filepath.Dir(objectPath), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(s.root, ".upload-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(temporary, hasher), io.LimitReader(body, metadata.ByteSize+1))
	if copyErr == nil {
		copyErr = temporary.Sync()
	}
	if closeErr := temporary.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return copyErr
	}
	if written != metadata.ByteSize || hex.EncodeToString(hasher.Sum(nil)) != metadata.SHA256 {
		return errors.New("Library object does not match upload constraints")
	}
	if err := os.Chmod(temporaryPath, 0o600); err != nil {
		return err
	}
	if err := replaceLocalLibraryFile(temporaryPath, objectPath); err != nil {
		return err
	}
	raw, _ := json.Marshal(metadata)
	metadataTemporary, err := os.CreateTemp(filepath.Dir(metadataPath), ".metadata-*")
	if err != nil {
		_ = os.Remove(objectPath)
		return err
	}
	metadataTemporaryPath := metadataTemporary.Name()
	defer os.Remove(metadataTemporaryPath)
	if _, err := metadataTemporary.Write(raw); err != nil {
		_ = metadataTemporary.Close()
		_ = os.Remove(objectPath)
		return err
	}
	if err := metadataTemporary.Sync(); err != nil {
		_ = metadataTemporary.Close()
		_ = os.Remove(objectPath)
		return err
	}
	if err := metadataTemporary.Close(); err != nil {
		_ = os.Remove(objectPath)
		return err
	}
	if err := os.Chmod(metadataTemporaryPath, 0o600); err != nil {
		_ = os.Remove(objectPath)
		return err
	}
	if err := replaceLocalLibraryFile(metadataTemporaryPath, metadataPath); err != nil {
		_ = os.Remove(objectPath)
		return err
	}
	return nil
}

func (s *LocalLibraryObjectStore) Head(_ context.Context, key string) (LibraryObjectMetadata, error) {
	objectPath, metadataPath, err := s.paths(key)
	if err != nil {
		return LibraryObjectMetadata{}, err
	}
	info, err := os.Stat(objectPath)
	if errors.Is(err, os.ErrNotExist) {
		return LibraryObjectMetadata{}, ErrLibraryObjectNotFound
	}
	if err != nil {
		return LibraryObjectMetadata{}, err
	}
	raw, err := os.ReadFile(metadataPath)
	if errors.Is(err, os.ErrNotExist) {
		return LibraryObjectMetadata{}, ErrLibraryObjectNotFound
	}
	if err != nil {
		return LibraryObjectMetadata{}, err
	}
	var metadata LibraryObjectMetadata
	if json.Unmarshal(raw, &metadata) != nil || metadata.ByteSize != info.Size() || validateLibraryObject(key, metadata) != nil {
		return LibraryObjectMetadata{}, errors.New("local Library object metadata is invalid")
	}
	return metadata, nil
}

func (s *LocalLibraryObjectStore) Open(ctx context.Context, key string) (io.ReadCloser, LibraryObjectMetadata, error) {
	metadata, err := s.Head(ctx, key)
	if err != nil {
		return nil, LibraryObjectMetadata{}, err
	}
	objectPath, _, _ := s.paths(key)
	file, err := os.Open(objectPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, LibraryObjectMetadata{}, ErrLibraryObjectNotFound
	}
	return file, metadata, err
}

func (s *LocalLibraryObjectStore) Delete(_ context.Context, key string) error {
	objectPath, metadataPath, err := s.paths(key)
	if err != nil {
		return err
	}
	if err := os.Remove(objectPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Remove(metadataPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

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

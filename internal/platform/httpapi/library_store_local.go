package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// LocalLibraryObjectStore is a persistent filesystem backend for development.
// Production always uses S3-compatible storage.
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

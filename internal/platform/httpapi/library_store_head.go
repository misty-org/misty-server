package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

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

type S3LibraryObjectStoreConfig struct {
	Endpoint           string
	Region             string
	Bucket             string
	AccessKeyID        string
	SecretAccessKey    string
	ForcePathStyle     bool
	BucketPrivate      bool
	PermanentObjects   bool
	AllowInsecureLocal bool
	HTTPClient         *http.Client
}

type libraryS3API interface {
	HeadBucket(context.Context, *s3.HeadBucketInput, ...func(*s3.Options)) (*s3.HeadBucketOutput, error)
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	HeadObject(context.Context, *s3.HeadObjectInput, ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	DeleteObject(context.Context, *s3.DeleteObjectInput, ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
	ListObjectsV2(context.Context, *s3.ListObjectsV2Input, ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
}

// libraryS3Presigner is satisfied by *s3.PresignClient. It is separate from
// libraryS3API so tests can substitute either half independently.
type libraryS3Presigner interface {
	PresignPutObject(context.Context, *s3.PutObjectInput, ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error)
	PresignGetObject(context.Context, *s3.GetObjectInput, ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error)
}

type S3LibraryObjectStore struct {
	TestingBucket string
	client        libraryS3API
	// presigner is nil only when a test injects a bare client; production
	// construction always sets it, and PresignPut/PresignGet fail closed.
	TestingPresigner libraryS3Presigner
}

func (s *S3LibraryObjectStore) Health(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(s.TestingBucket)})
	return err
}

func NewS3LibraryObjectStore(config S3LibraryObjectStoreConfig) (*S3LibraryObjectStore, error) {
	if err := validateS3LibraryObjectStoreConfig(config); err != nil {
		return nil, err
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = newLibraryHTTPClient()
	}
	options := s3.Options{
		Region:       strings.TrimSpace(config.Region),
		Credentials:  credentials.NewStaticCredentialsProvider(strings.TrimSpace(config.AccessKeyID), strings.TrimSpace(config.SecretAccessKey), ""),
		HTTPClient:   httpClient,
		UsePathStyle: config.ForcePathStyle,
		Retryer:      retry.NewStandard(func(options *retry.StandardOptions) { options.MaxAttempts = 3 }),
	}
	if endpoint := strings.TrimSpace(config.Endpoint); endpoint != "" {
		options.BaseEndpoint = aws.String(strings.TrimRight(endpoint, "/"))
	}
	client := s3.New(options)
	return &S3LibraryObjectStore{
		TestingBucket:    strings.TrimSpace(config.Bucket),
		client:           client,
		TestingPresigner: s3.NewPresignClient(client),
	}, nil
}

func validateS3LibraryObjectStoreConfig(config S3LibraryObjectStoreConfig) error {
	if strings.TrimSpace(config.Bucket) == "" || strings.ContainsAny(config.Bucket, "/\\\x00\r\n") {
		return errors.New("Library bucket is required and must not contain path separators")
	}
	if strings.TrimSpace(config.Region) == "" {
		return errors.New("Library S3 region is required (use auto for Cloudflare R2)")
	}
	if strings.TrimSpace(config.AccessKeyID) == "" || strings.TrimSpace(config.SecretAccessKey) == "" {
		return errors.New("Library S3 credentials are required")
	}
	if !config.BucketPrivate {
		return errors.New("Library bucket must be private")
	}
	if !config.PermanentObjects {
		return errors.New("Library objects must be excluded from the Agent attachment expiry rule")
	}
	if endpoint := strings.TrimSpace(config.Endpoint); endpoint != "" {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
			return errors.New("Library S3 endpoint must be an origin URL without credentials, path, query, or fragment")
		}
		if parsed.Scheme != "https" && !(config.AllowInsecureLocal && parsed.Scheme == "http" && isLoopbackLibraryHost(parsed.Hostname())) {
			return errors.New("Library S3 endpoint must use HTTPS")
		}
	}
	return nil
}

func (s *S3LibraryObjectStore) Put(ctx context.Context, key string, body io.Reader, metadata LibraryObjectMetadata) error {
	if err := validateLibraryObject(key, metadata); err != nil {
		return err
	}
	checksum, _ := hex.DecodeString(metadata.SHA256)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.TestingBucket), Key: aws.String(key), Body: body,
		ContentLength: aws.Int64(metadata.ByteSize), ContentType: aws.String(metadata.MIMEType),
		ChecksumSHA256: aws.String(base64.StdEncoding.EncodeToString(checksum)),
		Metadata:       map[string]string{TestingLibrarySHA256MetadataKey: metadata.SHA256},
	})
	return mapLibraryS3Error(err)
}

func (s *S3LibraryObjectStore) Head(ctx context.Context, key string) (LibraryObjectMetadata, error) {
	if !libraryObjectKeyPattern.MatchString(key) {
		return LibraryObjectMetadata{}, ErrLibraryObjectNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	result, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.TestingBucket), Key: aws.String(key)})
	if err != nil {
		return LibraryObjectMetadata{}, mapLibraryS3Error(err)
	}
	return libraryS3Metadata(result.ContentLength, result.ContentType, result.Metadata)
}

func (s *S3LibraryObjectStore) Open(ctx context.Context, key string) (io.ReadCloser, LibraryObjectMetadata, error) {
	if !libraryObjectKeyPattern.MatchString(key) {
		return nil, LibraryObjectMetadata{}, ErrLibraryObjectNotFound
	}
	ctx, cancel := context.WithCancel(ctx)
	result, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.TestingBucket), Key: aws.String(key)})
	if err != nil {
		cancel()
		return nil, LibraryObjectMetadata{}, mapLibraryS3Error(err)
	}
	metadata, err := libraryS3Metadata(result.ContentLength, result.ContentType, result.Metadata)
	if err != nil {
		cancel()
		_ = result.Body.Close()
		return nil, LibraryObjectMetadata{}, err
	}
	return &cancelOnCloseReadCloser{ReadCloser: result.Body, cancel: cancel}, metadata, nil
}

func (s *S3LibraryObjectStore) Delete(ctx context.Context, key string) error {
	if !libraryObjectKeyPattern.MatchString(key) {
		return ErrLibraryObjectNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.TestingBucket), Key: aws.String(key)})
	return mapLibraryS3Error(err)
}

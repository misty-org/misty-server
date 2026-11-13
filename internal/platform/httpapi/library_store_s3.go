package api

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"crypto/tls"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"net"

	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

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
	Bucket string
	client libraryS3API
	// presigner is nil only when a test injects a bare client; production
	// construction always sets it, and PresignPut/PresignGet fail closed.
	Presigner libraryS3Presigner
}

func (s *S3LibraryObjectStore) Health(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(s.Bucket)})
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
		Bucket:    strings.TrimSpace(config.Bucket),
		client:    client,
		Presigner: s3.NewPresignClient(client),
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
		Bucket: aws.String(s.Bucket), Key: aws.String(key), Body: body,
		ContentLength: aws.Int64(metadata.ByteSize), ContentType: aws.String(metadata.MIMEType),
		ChecksumSHA256: aws.String(base64.StdEncoding.EncodeToString(checksum)),
		Metadata:       map[string]string{LibrarySHA256MetadataKey: metadata.SHA256},
	})
	return mapLibraryS3Error(err)
}

func (s *S3LibraryObjectStore) Head(ctx context.Context, key string) (LibraryObjectMetadata, error) {
	if !libraryObjectKeyPattern.MatchString(key) {
		return LibraryObjectMetadata{}, ErrLibraryObjectNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	result, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.Bucket), Key: aws.String(key)})
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
	result, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.Bucket), Key: aws.String(key)})
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
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.Bucket), Key: aws.String(key)})
	return mapLibraryS3Error(err)
}

func isLoopbackLibraryHost(host string) bool {
	return strings.EqualFold(host, "localhost") || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()
}

func (s *S3LibraryObjectStore) List(
	ctx context.Context, prefix, cursor string, limit int,
) (LibraryObjectPage, error) {
	if prefix != "library/" {
		return LibraryObjectPage{}, errors.New("unsupported object inventory prefix")
	}
	if limit < 1 || limit > 1000 {
		limit = 250
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	input := &s3.ListObjectsV2Input{
		Bucket: aws.String(s.Bucket), Prefix: aws.String(prefix), MaxKeys: aws.Int32(int32(limit)),
	}
	if cursor != "" {
		input.ContinuationToken = aws.String(cursor)
	}
	result, err := s.client.ListObjectsV2(ctx, input)
	if err != nil {
		return LibraryObjectPage{}, mapLibraryS3Error(err)
	}
	page := LibraryObjectPage{Objects: make([]LibraryObjectEntry, 0, len(result.Contents))}
	for _, object := range result.Contents {
		key := aws.ToString(object.Key)
		if !libraryObjectKeyPattern.MatchString(key) {
			continue
		}
		page.Objects = append(page.Objects, LibraryObjectEntry{
			Key: key, ByteSize: aws.ToInt64(object.Size), LastModified: aws.ToTime(object.LastModified),
		})
	}
	if result.IsTruncated != nil && *result.IsTruncated {
		page.NextCursor = aws.ToString(result.NextContinuationToken)
	}
	return page, nil
}

func libraryS3Metadata(contentLength *int64, contentType *string, values map[string]string) (LibraryObjectMetadata, error) {
	if contentLength == nil || *contentLength < 1 {
		return LibraryObjectMetadata{}, errors.New("Library object has invalid size metadata")
	}
	checksum := strings.ToLower(strings.TrimSpace(values[LibrarySHA256MetadataKey]))
	if !sha256Pattern.MatchString(checksum) {
		return LibraryObjectMetadata{}, errors.New("Library object is missing verified checksum metadata")
	}
	return LibraryObjectMetadata{ByteSize: *contentLength, SHA256: checksum, MIMEType: aws.ToString(contentType)}, nil
}

func mapLibraryS3Error(err error) error {
	if err == nil {
		return nil
	}
	var responseError *smithyhttp.ResponseError
	if errors.As(err, &responseError) && responseError.HTTPStatusCode() == http.StatusNotFound {
		return ErrLibraryObjectNotFound
	}
	var apiError smithy.APIError
	if errors.As(err, &apiError) && (apiError.ErrorCode() == "NoSuchKey" || apiError.ErrorCode() == "NotFound") {
		return ErrLibraryObjectNotFound
	}
	return fmt.Errorf("Library object store: %w", err)
}

func newLibraryHTTPClient() *http.Client {
	return &http.Client{Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: time.Second,
		IdleConnTimeout:       60 * time.Second,
		MaxIdleConns:          50,
		MaxIdleConnsPerHost:   10,
	}}
}

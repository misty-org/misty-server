package api

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"iter"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
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
	Transport          http.RoundTripper
}

type libraryS3API interface {
	BucketExists(ctx context.Context, bucketName string) (bool, error)
	PutObject(ctx context.Context, bucketName, objectName string, data io.Reader, size int64, md5Base64, sha256Hex string, opts minio.PutObjectOptions) (minio.UploadInfo, error)
	StatObject(ctx context.Context, bucketName, objectName string, opts minio.StatObjectOptions) (minio.ObjectInfo, error)
	GetObject(ctx context.Context, bucketName, objectName string, opts minio.GetObjectOptions) (io.ReadCloser, minio.ObjectInfo, http.Header, error)
	RemoveObject(ctx context.Context, bucketName, objectName string, opts minio.RemoveObjectOptions) error
	ListObjectsIter(ctx context.Context, bucketName string, opts minio.ListObjectsOptions) iter.Seq[minio.ObjectInfo]
}

type libraryS3Presigner interface {
	PresignHeader(ctx context.Context, method, bucketName, objectName string, expires time.Duration, reqParams url.Values, extraHeaders http.Header) (*url.URL, error)
}

type S3LibraryObjectStore struct {
	Bucket    string
	client    libraryS3API
	Presigner libraryS3Presigner
}

const (
	libraryS3ChecksumSHA256Header = "x-amz-checksum-sha256"
	libraryS3ChecksumModeHeader   = "x-amz-checksum-mode"
	libraryS3MetadataHeaderPrefix = "x-amz-meta-"
)

var libraryS3ResponseChecksums = []minio.ChecksumType{
	minio.ChecksumCRC64NVME,
	minio.ChecksumCRC32,
	minio.ChecksumCRC32C,
	minio.ChecksumSHA256,
	minio.ChecksumSHA1,
	minio.ChecksumSHA512,
	minio.ChecksumMD5,
	minio.ChecksumXXHash64,
	minio.ChecksumXXHash3,
	minio.ChecksumXXHash128,
}

func (s *S3LibraryObjectStore) Health(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	exists, err := s.client.BucketExists(ctx, s.Bucket)
	if err != nil {
		return err
	}
	if !exists {
		return errors.New("Library bucket does not exist")
	}
	return nil
}

func NewS3LibraryObjectStore(config S3LibraryObjectStoreConfig) (*S3LibraryObjectStore, error) {
	if err := validateS3LibraryObjectStoreConfig(config); err != nil {
		return nil, err
	}
	transport := config.Transport
	if transport == nil {
		transport = newLibraryTransport()
	}
	region := strings.TrimSpace(config.Region)
	endpoint, secure := libraryS3Endpoint(config.Endpoint, region)
	lookup := minio.BucketLookupDNS
	if config.ForcePathStyle {
		lookup = minio.BucketLookupPath
	}
	client, err := minio.NewCore(endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(strings.TrimSpace(config.AccessKeyID), strings.TrimSpace(config.SecretAccessKey), ""),
		Secure:       secure,
		Transport:    transport,
		Region:       region,
		BucketLookup: lookup,
		MaxRetries:   3,
	})
	if err != nil {
		return nil, fmt.Errorf("Library S3 client: %w", err)
	}
	return &S3LibraryObjectStore{
		Bucket:    strings.TrimSpace(config.Bucket),
		client:    client,
		Presigner: client,
	}, nil
}

func libraryS3Endpoint(endpoint, region string) (string, bool) {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if endpoint == "" {
		return "s3." + region + ".amazonaws.com", true
	}
	parsed, _ := url.Parse(endpoint)
	return parsed.Host, parsed.Scheme == "https"
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
	_, err := s.client.PutObject(ctx, s.Bucket, key, body, metadata.ByteSize, "", "", minio.PutObjectOptions{
		ContentType: metadata.MIMEType,
		UserMetadata: map[string]string{
			LibrarySHA256MetadataKey:      metadata.SHA256,
			libraryS3ChecksumSHA256Header: base64.StdEncoding.EncodeToString(checksum),
		},
		DisableContentSha256: true,
	})
	return mapLibraryS3Error(err)
}

func (s *S3LibraryObjectStore) Head(ctx context.Context, key string) (LibraryObjectMetadata, error) {
	if !libraryObjectKeyPattern.MatchString(key) {
		return LibraryObjectMetadata{}, ErrLibraryObjectNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	info, err := s.client.StatObject(ctx, s.Bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return LibraryObjectMetadata{}, mapLibraryS3Error(err)
	}
	return libraryS3Metadata(info.Size, info.Metadata)
}

func (s *S3LibraryObjectStore) Open(ctx context.Context, key string) (io.ReadCloser, LibraryObjectMetadata, error) {
	if !libraryObjectKeyPattern.MatchString(key) {
		return nil, LibraryObjectMetadata{}, ErrLibraryObjectNotFound
	}
	ctx, cancel := context.WithCancel(ctx)
	options := minio.GetObjectOptions{}
	options.Set(libraryS3ChecksumModeHeader, "ENABLED")
	body, info, header, err := s.client.GetObject(ctx, s.Bucket, key, options)
	if err != nil {
		cancel()
		return nil, LibraryObjectMetadata{}, mapLibraryS3Error(err)
	}
	metadata, err := libraryS3Metadata(info.Size, header)
	if err != nil {
		cancel()
		_ = body.Close()
		return nil, LibraryObjectMetadata{}, err
	}
	return &cancelOnCloseReadCloser{ReadCloser: verifiedLibraryS3Body(body, header), cancel: cancel}, metadata, nil
}

func (s *S3LibraryObjectStore) Delete(ctx context.Context, key string) error {
	if !libraryObjectKeyPattern.MatchString(key) {
		return ErrLibraryObjectNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return mapLibraryS3Error(s.client.RemoveObject(ctx, s.Bucket, key, minio.RemoveObjectOptions{}))
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
	fetchOwner := false
	objects := s.client.ListObjectsIter(ctx, s.Bucket, minio.ListObjectsOptions{
		Prefix: prefix, Recursive: true, StartAfter: cursor, MaxKeys: min(limit+1, 1000), FetchOwner: &fetchOwner,
	})
	page := LibraryObjectPage{Objects: make([]LibraryObjectEntry, 0, limit)}
	listed, last := 0, ""
	for object := range objects {
		if object.Err != nil {
			return LibraryObjectPage{}, mapLibraryS3Error(object.Err)
		}
		if listed == limit {
			page.NextCursor = last
			return page, nil
		}
		listed, last = listed+1, object.Key
		if !libraryObjectKeyPattern.MatchString(object.Key) {
			continue
		}
		page.Objects = append(page.Objects, LibraryObjectEntry{
			Key: object.Key, ByteSize: object.Size, LastModified: object.LastModified,
		})
	}
	if err := ctx.Err(); err != nil {
		return LibraryObjectPage{}, mapLibraryS3Error(err)
	}
	return page, nil
}

func libraryS3Metadata(size int64, header http.Header) (LibraryObjectMetadata, error) {
	if size < 1 {
		return LibraryObjectMetadata{}, errors.New("Library object has invalid size metadata")
	}
	checksum := strings.ToLower(strings.TrimSpace(header.Get(libraryS3MetadataHeaderPrefix + LibrarySHA256MetadataKey)))
	if !sha256Pattern.MatchString(checksum) {
		return LibraryObjectMetadata{}, errors.New("Library object is missing verified checksum metadata")
	}
	return LibraryObjectMetadata{ByteSize: size, SHA256: checksum, MIMEType: strings.TrimSpace(header.Get("Content-Type"))}, nil
}

func verifiedLibraryS3Body(body io.ReadCloser, header http.Header) io.ReadCloser {
	var algorithm minio.ChecksumType
	expected := ""
	for _, candidate := range libraryS3ResponseChecksums {
		if value := header.Get(candidate.Key()); value != "" {
			algorithm, expected = candidate, value
		}
	}
	if expected == "" || strings.Contains(expected, "-") {
		return body
	}
	return &libraryS3ChecksumReader{ReadCloser: body, hash: algorithm.Hasher(), algorithm: algorithm, expected: expected}
}

type libraryS3ChecksumReader struct {
	io.ReadCloser
	hash      hash.Hash
	algorithm minio.ChecksumType
	expected  string
}

func (reader *libraryS3ChecksumReader) Read(p []byte) (int, error) {
	n, err := reader.ReadCloser.Read(p)
	reader.hash.Write(p[:n])
	if err == io.EOF {
		actual := base64.StdEncoding.EncodeToString(reader.hash.Sum(nil))
		if !strings.EqualFold(reader.expected, actual) {
			return n, fmt.Errorf("checksum did not match: algorithm %s, expect %s, actual %s", reader.algorithm, reader.expected, actual)
		}
	}
	return n, err
}

func mapLibraryS3Error(err error) error {
	if err == nil {
		return nil
	}
	var response minio.ErrorResponse
	if errors.As(err, &response) && (response.StatusCode == http.StatusNotFound ||
		response.Code == minio.NoSuchKey || response.Code == minio.NoSuchUpload || response.Code == "NotFound") {
		return ErrLibraryObjectNotFound
	}
	return fmt.Errorf("Library object store: %w", err)
}

func newLibraryTransport() *http.Transport {
	return &http.Transport{
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
		DisableCompression:    true,
	}
}

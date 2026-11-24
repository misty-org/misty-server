package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/minio/minio-go/v7"
)

type LibraryUploadPart struct {
	Number int32  `json:"number"`
	ETag   string `json:"etag"`
}

type LibraryMultipartUploader interface {
	CreateMultipartUpload(ctx context.Context, key string, metadata LibraryObjectMetadata) (string, error)
	PresignUploadPart(ctx context.Context, key, uploadID string, number int32, size int64, ttl time.Duration) (string, error)
	CompleteMultipartUpload(ctx context.Context, key, uploadID string, parts []LibraryUploadPart) error
	AbortMultipartUpload(ctx context.Context, key, uploadID string) error
}

type libraryS3MultipartAPI interface {
	NewMultipartUpload(ctx context.Context, bucketName, objectName string, opts minio.PutObjectOptions) (string, error)
	CompleteMultipartUpload(ctx context.Context, bucketName, objectName, uploadID string, parts []minio.CompletePart, opts minio.PutObjectOptions) (minio.UploadInfo, error)
	AbortMultipartUpload(ctx context.Context, bucketName, objectName, uploadID string) error
}

var errLibraryMultipartUnavailable = errors.New("Library object store can't take multipart uploads")

func (s *S3LibraryObjectStore) multipartClients() (libraryS3MultipartAPI, libraryS3Presigner, error) {
	client, ok := s.client.(libraryS3MultipartAPI)
	if !ok || s.Presigner == nil {
		return nil, nil, errLibraryMultipartUnavailable
	}
	return client, s.Presigner, nil
}

func (s *S3LibraryObjectStore) CreateMultipartUpload(ctx context.Context, key string, metadata LibraryObjectMetadata) (string, error) {
	if !libraryObjectKeyPattern.MatchString(key) || metadata.ByteSize < 1 || !sha256Pattern.MatchString(metadata.SHA256) {
		return "", errors.New("invalid Library object constraints")
	}
	client, _, err := s.multipartClients()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	uploadID, err := client.NewMultipartUpload(ctx, s.Bucket, key, minio.PutObjectOptions{
		ContentType:  metadata.MIMEType,
		UserMetadata: map[string]string{LibrarySHA256MetadataKey: metadata.SHA256},
	})
	if err != nil {
		return "", mapLibraryS3Error(err)
	}
	return uploadID, nil
}

func (s *S3LibraryObjectStore) PresignUploadPart(ctx context.Context, key, uploadID string, number int32, size int64, ttl time.Duration) (string, error) {
	if !libraryObjectKeyPattern.MatchString(key) || uploadID == "" || number < 1 || number > 10000 || size < 1 {
		return "", errors.New("invalid Library upload part")
	}
	ttl, err := validatePresignTTL(ttl)
	if err != nil {
		return "", err
	}
	_, presigner, err := s.multipartClients()
	if err != nil {
		return "", err
	}
	query := url.Values{}
	query.Set("partNumber", strconv.Itoa(int(number)))
	query.Set("uploadId", uploadID)
	headers := http.Header{}
	headers.Set("Content-Length", strconv.FormatInt(size, 10))
	signed, err := presigner.PresignHeader(ctx, http.MethodPut, s.Bucket, key, ttl, query, headers)
	if err != nil {
		return "", mapLibraryS3Error(err)
	}
	return signed.String(), nil
}

func (s *S3LibraryObjectStore) CompleteMultipartUpload(ctx context.Context, key, uploadID string, parts []LibraryUploadPart) error {
	if !libraryObjectKeyPattern.MatchString(key) || uploadID == "" || len(parts) == 0 {
		return errors.New("invalid Library multipart completion")
	}
	client, _, err := s.multipartClients()
	if err != nil {
		return err
	}
	sorted := append([]LibraryUploadPart(nil), parts...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Number < sorted[j].Number })
	completed := make([]minio.CompletePart, len(sorted))
	for i, part := range sorted {
		completed[i] = minio.CompletePart{PartNumber: int(part.Number), ETag: part.ETag}
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	_, err = client.CompleteMultipartUpload(ctx, s.Bucket, key, uploadID, completed, minio.PutObjectOptions{})
	return mapLibraryS3Error(err)
}

func (s *S3LibraryObjectStore) AbortMultipartUpload(ctx context.Context, key, uploadID string) error {
	if !libraryObjectKeyPattern.MatchString(key) || uploadID == "" {
		return ErrLibraryObjectNotFound
	}
	client, _, err := s.multipartClients()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return mapLibraryS3Error(client.AbortMultipartUpload(ctx, s.Bucket, key, uploadID))
}

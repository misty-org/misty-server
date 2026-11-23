package api

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type PresignedTransfer struct {
	URL       string            `json:"url"`
	Method    string            `json:"method"`
	Headers   map[string]string `json:"headers"`
	ExpiresAt time.Time         `json:"expires_at"`
}

type PresignedDownload struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
	Filename  string    `json:"filename"`
	MIMEType  string    `json:"mime_type,omitempty"`
	ByteSize  int64     `json:"byte_size,omitempty"`
	SHA256    string    `json:"sha256,omitempty"`
}

type LibraryObjectPresigner interface {
	PresignPut(ctx context.Context, key string, metadata LibraryObjectMetadata, ttl time.Duration) (PresignedTransfer, error)
	PresignGet(ctx context.Context, key, filename string, ttl time.Duration) (PresignedDownload, error)
}

const (
	minPresignTTL = 30 * time.Second
	maxPresignTTL = 60 * time.Minute
)

func validatePresignTTL(ttl time.Duration) (time.Duration, error) {
	if ttl < minPresignTTL || ttl > maxPresignTTL {
		return 0, fmt.Errorf("presigned URL TTL must be between %s and %s", minPresignTTL, maxPresignTTL)
	}
	return ttl, nil
}

func safeContentDisposition(filename string) string {
	filename = sanitizeLibraryFilename(filename)
	if filename == "" {
		filename = "download"
	}
	ascii := strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, filename)
	if ascii == filename {
		return fmt.Sprintf("attachment; filename=%q", ascii)
	}
	return fmt.Sprintf("attachment; filename=%q; filename*=%s", ascii, mime.FormatMediaType("UTF-8''"+url.PathEscape(filename), nil))
}

func (s *S3LibraryObjectStore) PresignPut(ctx context.Context, key string, metadata LibraryObjectMetadata, ttl time.Duration) (PresignedTransfer, error) {
	if err := validateLibraryObject(key, metadata); err != nil {
		return PresignedTransfer{}, err
	}
	ttl, err := validatePresignTTL(ttl)
	if err != nil {
		return PresignedTransfer{}, err
	}
	checksum, err := hex.DecodeString(metadata.SHA256)
	if err != nil {
		return PresignedTransfer{}, errors.New("invalid Library object checksum")
	}
	encodedChecksum := base64.StdEncoding.EncodeToString(checksum)
	presigned, err := s.Presigner.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:         aws.String(s.Bucket),
		Key:            aws.String(key),
		ContentLength:  aws.Int64(metadata.ByteSize),
		ContentType:    aws.String(metadata.MIMEType),
		ChecksumSHA256: aws.String(encodedChecksum),
		Metadata:       map[string]string{LibrarySHA256MetadataKey: metadata.SHA256},
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return PresignedTransfer{}, mapLibraryS3Error(err)
	}
	headers := map[string]string{}
	addPresignedTransferHeader(headers, presigned.SignedHeader, "Content-Type", metadata.MIMEType)
	addPresignedTransferHeader(headers, presigned.SignedHeader, "x-amz-checksum-sha256", encodedChecksum)
	addPresignedTransferHeader(headers, presigned.SignedHeader, "x-amz-meta-"+LibrarySHA256MetadataKey, metadata.SHA256)
	return PresignedTransfer{
		URL:       presigned.URL,
		Method:    presigned.Method,
		Headers:   headers,
		ExpiresAt: time.Now().Add(ttl).UTC(),
	}, nil
}

func addPresignedTransferHeader(headers map[string]string, signed http.Header, name, value string) {
	if value == "" || !presignedTransferHeaderSigned(signed, name) {
		return
	}
	headers[name] = value
}

func presignedTransferHeaderSigned(signed http.Header, name string) bool {
	for key := range signed {
		if strings.EqualFold(key, name) {
			return true
		}
	}
	return false
}

func (s *S3LibraryObjectStore) PresignGet(ctx context.Context, key, filename string, ttl time.Duration) (PresignedDownload, error) {
	if !libraryObjectKeyPattern.MatchString(key) {
		return PresignedDownload{}, ErrLibraryObjectNotFound
	}
	ttl, err := validatePresignTTL(ttl)
	if err != nil {
		return PresignedDownload{}, err
	}
	disposition := safeContentDisposition(filename)
	presigned, err := s.Presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket:                     aws.String(s.Bucket),
		Key:                        aws.String(key),
		ResponseContentDisposition: aws.String(disposition),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return PresignedDownload{}, mapLibraryS3Error(err)
	}
	return PresignedDownload{
		URL:       presigned.URL,
		ExpiresAt: time.Now().Add(ttl).UTC(),
		Filename:  sanitizeLibraryFilename(filename),
	}, nil
}

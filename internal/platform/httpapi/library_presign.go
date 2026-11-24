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
	"strconv"
	"strings"
	"time"
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
	query := url.Values{}
	query.Set("X-Amz-Checksum-Sha256", encodedChecksum)
	signedHeaders := http.Header{}
	signedHeaders.Set("Content-Length", strconv.FormatInt(metadata.ByteSize, 10))
	if metadata.MIMEType != "" {
		signedHeaders.Set("Content-Type", metadata.MIMEType)
	}
	signedHeaders.Set(libraryS3MetadataHeaderPrefix+LibrarySHA256MetadataKey, metadata.SHA256)
	presigned, err := s.Presigner.PresignHeader(ctx, http.MethodPut, s.Bucket, key, ttl, query, signedHeaders)
	if err != nil {
		return PresignedTransfer{}, mapLibraryS3Error(err)
	}
	signed := presignedSignedHeaders(presigned)
	headers := map[string]string{}
	addPresignedTransferHeader(headers, signed, "Content-Type", metadata.MIMEType)
	addPresignedTransferHeader(headers, signed, libraryS3ChecksumSHA256Header, encodedChecksum)
	addPresignedTransferHeader(headers, signed, libraryS3MetadataHeaderPrefix+LibrarySHA256MetadataKey, metadata.SHA256)
	return PresignedTransfer{
		URL:       presigned.String(),
		Method:    http.MethodPut,
		Headers:   headers,
		ExpiresAt: time.Now().Add(ttl).UTC(),
	}, nil
}

func presignedSignedHeaders(presigned *url.URL) map[string]bool {
	signed := map[string]bool{}
	for _, name := range strings.Split(presigned.Query().Get("X-Amz-SignedHeaders"), ";") {
		if name = strings.ToLower(strings.TrimSpace(name)); name != "" {
			signed[name] = true
		}
	}
	return signed
}

func addPresignedTransferHeader(headers map[string]string, signed map[string]bool, name, value string) {
	if value == "" || !signed[strings.ToLower(name)] {
		return
	}
	headers[name] = value
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
	query := url.Values{}
	query.Set("response-content-disposition", disposition)
	presigned, err := s.Presigner.PresignHeader(ctx, http.MethodGet, s.Bucket, key, ttl, query, nil)
	if err != nil {
		return PresignedDownload{}, mapLibraryS3Error(err)
	}
	return PresignedDownload{
		URL:       presigned.String(),
		ExpiresAt: time.Now().Add(ttl).UTC(),
		Filename:  sanitizeLibraryFilename(filename),
	}, nil
}

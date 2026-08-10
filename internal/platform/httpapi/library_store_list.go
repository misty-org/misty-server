package api

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

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
		Bucket: aws.String(s.TestingBucket), Prefix: aws.String(prefix), MaxKeys: aws.Int32(int32(limit)),
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

func validateLibraryObject(key string, metadata LibraryObjectMetadata) error {
	if !libraryObjectKeyPattern.MatchString(key) || metadata.ByteSize < 1 || metadata.ByteSize > 1_000_000_000 || !sha256Pattern.MatchString(metadata.SHA256) {
		return errors.New("invalid Library object constraints")
	}
	return nil
}

func libraryS3Metadata(contentLength *int64, contentType *string, values map[string]string) (LibraryObjectMetadata, error) {
	if contentLength == nil || *contentLength < 1 {
		return LibraryObjectMetadata{}, errors.New("Library object has invalid size metadata")
	}
	checksum := strings.ToLower(strings.TrimSpace(values[TestingLibrarySHA256MetadataKey]))
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

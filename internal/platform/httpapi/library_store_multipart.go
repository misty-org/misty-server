package api

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// Large drive files go to R2 as S3 multipart uploads: the server starts the
// upload, signs a PUT for each part, and completes it from the parts' ETags.
// The bytes still go straight between the app and R2. Each part's URL is
// signed for its exact length, and the object carries the server's checksum
// metadata from the start; R2 can't check a whole-object SHA-256 of a
// multipart upload, so the digest is the uploader's word, checked by every
// computer that downloads the file.

// LibraryUploadPart is one part of a multipart upload as the app reports it.
type LibraryUploadPart struct {
	Number int32  `json:"number"`
	ETag   string `json:"etag"`
}

// LibraryMultipartUploader is the optional capability of a store that takes
// multipart uploads. The development stores don't have it, and the drive
// refuses large files without it.
type LibraryMultipartUploader interface {
	CreateMultipartUpload(ctx context.Context, key string, metadata LibraryObjectMetadata) (string, error)
	PresignUploadPart(ctx context.Context, key, uploadID string, number int32, size int64, ttl time.Duration) (string, error)
	CompleteMultipartUpload(ctx context.Context, key, uploadID string, parts []LibraryUploadPart) error
	AbortMultipartUpload(ctx context.Context, key, uploadID string) error
}

// libraryS3MultipartAPI is the part of *s3.Client the multipart calls use.
type libraryS3MultipartAPI interface {
	CreateMultipartUpload(context.Context, *s3.CreateMultipartUploadInput, ...func(*s3.Options)) (*s3.CreateMultipartUploadOutput, error)
	CompleteMultipartUpload(context.Context, *s3.CompleteMultipartUploadInput, ...func(*s3.Options)) (*s3.CompleteMultipartUploadOutput, error)
	AbortMultipartUpload(context.Context, *s3.AbortMultipartUploadInput, ...func(*s3.Options)) (*s3.AbortMultipartUploadOutput, error)
}

// libraryS3PartPresigner is the part of *s3.PresignClient that signs part uploads.
type libraryS3PartPresigner interface {
	PresignUploadPart(context.Context, *s3.UploadPartInput, ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error)
}

var errLibraryMultipartUnavailable = errors.New("Library object store can't take multipart uploads")

func (s *S3LibraryObjectStore) multipartClients() (libraryS3MultipartAPI, libraryS3PartPresigner, error) {
	client, ok := s.client.(libraryS3MultipartAPI)
	presigner, signs := s.Presigner.(libraryS3PartPresigner)
	if !ok || !signs {
		return nil, nil, errLibraryMultipartUnavailable
	}
	return client, presigner, nil
}

// CreateMultipartUpload starts a multipart upload of one object, with the
// content type and checksum metadata a single PUT would carry.
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
	out, err := client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket: aws.String(s.Bucket), Key: aws.String(key), ContentType: aws.String(metadata.MIMEType),
		Metadata: map[string]string{LibrarySHA256MetadataKey: metadata.SHA256},
	})
	if err != nil {
		return "", mapLibraryS3Error(err)
	}
	return aws.ToString(out.UploadId), nil
}

// PresignUploadPart signs one part's PUT for its exact length.
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
	signed, err := presigner.PresignUploadPart(ctx, &s3.UploadPartInput{
		Bucket: aws.String(s.Bucket), Key: aws.String(key), UploadId: aws.String(uploadID),
		PartNumber: aws.Int32(number), ContentLength: aws.Int64(size),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", mapLibraryS3Error(err)
	}
	return signed.URL, nil
}

// CompleteMultipartUpload joins the parts, in order, into the object.
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
	completed := make([]types.CompletedPart, len(sorted))
	for i, part := range sorted {
		completed[i] = types.CompletedPart{PartNumber: aws.Int32(part.Number), ETag: aws.String(part.ETag)}
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	_, err = client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket: aws.String(s.Bucket), Key: aws.String(key), UploadId: aws.String(uploadID),
		MultipartUpload: &types.CompletedMultipartUpload{Parts: completed},
	})
	return mapLibraryS3Error(err)
}

// AbortMultipartUpload gives up a multipart upload and the parts it holds.
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
	_, err = client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket: aws.String(s.Bucket), Key: aws.String(key), UploadId: aws.String(uploadID),
	})
	return mapLibraryS3Error(err)
}

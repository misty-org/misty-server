package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	. "github.com/misty-org/misty-server/internal/platform/httpapi"

	"github.com/misty-org/misty-server/internal/platform/db"
)

type recordingPresigner struct {
	method        string
	bucket        string
	object        string
	expires       time.Duration
	query         url.Values
	headers       http.Header
	signedHeaders string
}

func (p *recordingPresigner) PresignHeader(_ context.Context, method, bucket, object string, expires time.Duration, query url.Values, headers http.Header) (*url.URL, error) {
	p.method, p.bucket, p.object, p.expires, p.query, p.headers = method, bucket, object, expires, query, headers
	signed := p.signedHeaders
	if signed == "" {
		names := []string{"host"}
		for name := range headers {
			names = append(names, strings.ToLower(name))
		}
		sort.Strings(names)
		signed = strings.Join(names, ";")
	}
	values := url.Values{}
	for name, value := range query {
		values[name] = value
	}
	values.Set("X-Amz-SignedHeaders", signed)
	return &url.URL{Scheme: "https", Host: "r2.example", Path: "/" + bucket + "/" + object, RawQuery: values.Encode()}, nil
}

func testObjectMetadata(t *testing.T) LibraryObjectMetadata {
	t.Helper()
	digest := sha256.Sum256([]byte("presign fixture"))
	return LibraryObjectMetadata{ByteSize: 15, SHA256: hex.EncodeToString(digest[:]), MIMEType: "text/plain"}
}

func testObjectChecksum(t *testing.T, metadata LibraryObjectMetadata) string {
	t.Helper()
	checksum, err := hex.DecodeString(metadata.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(checksum)
}

func testR2Store(t *testing.T) *S3LibraryObjectStore {
	t.Helper()
	store, err := NewS3LibraryObjectStore(S3LibraryObjectStoreConfig{
		Endpoint: "https://account.r2.cloudflarestorage.com", Region: "auto", Bucket: "misty",
		AccessKeyID: "access", SecretAccessKey: "secret",
		ForcePathStyle: true, BucketPrivate: true, PermanentObjects: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestPresignPutBindsKeySizeAndChecksum(t *testing.T) {
	presigner := &recordingPresigner{}
	store := &S3LibraryObjectStore{Bucket: "misty", Presigner: presigner}
	metadata := testObjectMetadata(t)

	transfer, err := store.PresignPut(context.Background(), "library/presignfixture1", metadata, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(transfer.URL, "https://r2.example/misty/library/presignfixture1?") || transfer.Method != http.MethodPut {
		t.Fatalf("PresignPut() = %#v", transfer)
	}
	if presigner.method != http.MethodPut || presigner.bucket != "misty" || presigner.object != "library/presignfixture1" {
		t.Fatalf("signed %s %s/%s", presigner.method, presigner.bucket, presigner.object)
	}
	if presigner.expires != 15*time.Minute {
		t.Fatalf("signed expiry = %s, want 15m", presigner.expires)
	}
	if presigner.headers.Get("Content-Length") != "15" {
		t.Fatalf("signed length = %q, want 15", presigner.headers.Get("Content-Length"))
	}
	wantChecksum := testObjectChecksum(t, metadata)
	if presigner.query.Get("X-Amz-Checksum-Sha256") != wantChecksum {
		t.Fatalf("signed checksum = %q, want %q", presigner.query.Get("X-Amz-Checksum-Sha256"), wantChecksum)
	}
	if presigner.headers.Get("Content-Type") != metadata.MIMEType ||
		presigner.headers.Get("x-amz-meta-"+LibrarySHA256MetadataKey) != metadata.SHA256 {
		t.Fatalf("signed headers = %#v", presigner.headers)
	}
	if transfer.Headers["Content-Type"] != metadata.MIMEType ||
		transfer.Headers["x-amz-meta-"+LibrarySHA256MetadataKey] != metadata.SHA256 {
		t.Fatalf("headers = %#v", transfer.Headers)
	}
	if _, ok := transfer.Headers["x-amz-checksum-sha256"]; ok {
		t.Fatalf("hoisted checksum leaked into transfer headers: %#v", transfer.Headers)
	}
	if len(transfer.Headers) != 2 {
		t.Fatalf("unexpected extra signed headers: %#v", transfer.Headers)
	}
	if !transfer.ExpiresAt.After(time.Now()) {
		t.Fatalf("ExpiresAt = %s, want future", transfer.ExpiresAt)
	}
}

func TestPresignPutReturnsChecksumHeaderWhenPresignerSignsIt(t *testing.T) {
	metadata := testObjectMetadata(t)
	wantChecksum := testObjectChecksum(t, metadata)
	presigner := &recordingPresigner{
		signedHeaders: "content-length;content-type;host;x-amz-checksum-sha256;x-amz-meta-misty-library-sha256",
	}
	store := &S3LibraryObjectStore{Bucket: "misty", Presigner: presigner}

	transfer, err := store.PresignPut(context.Background(), "library/presignfixture1", metadata, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	if transfer.Headers["x-amz-checksum-sha256"] != wantChecksum {
		t.Fatalf("checksum header = %q, want %q", transfer.Headers["x-amz-checksum-sha256"], wantChecksum)
	}
	if _, ok := transfer.Headers["Host"]; ok {
		t.Fatalf("browser-controlled header leaked into transfer headers: %#v", transfer.Headers)
	}
	if _, ok := transfer.Headers["Content-Length"]; ok {
		t.Fatalf("browser-controlled header leaked into transfer headers: %#v", transfer.Headers)
	}
}

func TestPresignRejectsBadKeyAndTTL(t *testing.T) {
	store := &S3LibraryObjectStore{Bucket: "misty", Presigner: &recordingPresigner{}}
	metadata := testObjectMetadata(t)

	if _, err := store.PresignPut(context.Background(), "../etc/passwd", metadata, time.Minute); err == nil {
		t.Fatal("PresignPut() accepted a key outside the permanent prefix")
	}
	if _, err := store.PresignGet(context.Background(), "notes/secret", "a.txt", time.Minute); err == nil {
		t.Fatal("PresignGet() accepted a key outside the permanent prefix")
	}
	if _, err := store.PresignPut(context.Background(), "library/presignfixture1", metadata, 24*time.Hour); err == nil {
		t.Fatal("PresignPut() accepted an out-of-range TTL")
	}
	if _, err := store.PresignGet(context.Background(), "library/presignfixture1", "a.txt", time.Second); err == nil {
		t.Fatal("PresignGet() accepted an out-of-range TTL")
	}
}

func TestPresignGetForcesSafeAttachmentDisposition(t *testing.T) {
	presigner := &recordingPresigner{}
	store := &S3LibraryObjectStore{Bucket: "misty", Presigner: presigner}

	download, err := store.PresignGet(context.Background(), "library/presignfixture1", "../../report\r\n.pdf", 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	if presigner.method != http.MethodGet || presigner.expires != 2*time.Minute || len(presigner.headers) != 0 {
		t.Fatalf("signed %s for %s with headers %#v", presigner.method, presigner.expires, presigner.headers)
	}
	disposition := presigner.query.Get("response-content-disposition")
	if !strings.HasPrefix(disposition, "attachment;") {
		t.Fatalf("disposition = %q, want attachment", disposition)
	}
	if strings.ContainsAny(disposition, "\r\n") || strings.Contains(disposition, "..") {
		t.Fatalf("disposition leaked path or newline: %q", disposition)
	}
	if download.Filename != "report.pdf" {
		t.Fatalf("Filename = %q, want report.pdf", download.Filename)
	}
}

func TestR2PresignPutSignsTheObjectHeadersAndHoistsTheChecksum(t *testing.T) {
	store := testR2Store(t)
	metadata := testObjectMetadata(t)

	transfer, err := store.PresignPut(context.Background(), "library/presignfixture1", metadata, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	signed, err := url.Parse(transfer.URL)
	if err != nil {
		t.Fatal(err)
	}
	if signed.Scheme != "https" || signed.Host != "account.r2.cloudflarestorage.com" || signed.Path != "/misty/library/presignfixture1" {
		t.Fatalf("URL = %s, want a path-style R2 URL", transfer.URL)
	}
	query := signed.Query()
	if got := query.Get("X-Amz-SignedHeaders"); got != "content-length;content-type;host;x-amz-meta-misty-library-sha256" {
		t.Fatalf("signed headers = %q", got)
	}
	if got := query.Get("X-Amz-Checksum-Sha256"); got != testObjectChecksum(t, metadata) {
		t.Fatalf("hoisted checksum = %q", got)
	}
	if query.Get("X-Amz-Expires") != "900" || !strings.HasSuffix(query.Get("X-Amz-Credential"), "/auto/s3/aws4_request") ||
		!strings.HasPrefix(query.Get("X-Amz-Credential"), "access/") || query.Get("X-Amz-Signature") == "" {
		t.Fatalf("signature query = %#v", query)
	}
	if transfer.Method != http.MethodPut || len(transfer.Headers) != 2 ||
		transfer.Headers["Content-Type"] != metadata.MIMEType ||
		transfer.Headers["x-amz-meta-"+LibrarySHA256MetadataKey] != metadata.SHA256 {
		t.Fatalf("transfer = %#v", transfer)
	}
}

func TestR2PresignGetSignsOnlyTheHostAndTheDisposition(t *testing.T) {
	store := testR2Store(t)

	download, err := store.PresignGet(context.Background(), "library/presignfixture1", "Quarterly report.pdf", 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	signed, err := url.Parse(download.URL)
	if err != nil {
		t.Fatal(err)
	}
	query := signed.Query()
	if signed.Path != "/misty/library/presignfixture1" || query.Get("X-Amz-SignedHeaders") != "host" || query.Get("X-Amz-Expires") != "120" {
		t.Fatalf("URL = %s", download.URL)
	}
	if got := query.Get("response-content-disposition"); got != `attachment; filename="Quarterly report.pdf"` {
		t.Fatalf("disposition = %q", got)
	}
}

func TestR2PresignUploadPartBindsThePartSize(t *testing.T) {
	store := testR2Store(t)

	partURL, err := store.PresignUploadPart(context.Background(), "library/presignfixture1", "upload/1+2", 3, 5<<20, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	signed, err := url.Parse(partURL)
	if err != nil {
		t.Fatal(err)
	}
	query := signed.Query()
	if signed.Path != "/misty/library/presignfixture1" || query.Get("X-Amz-SignedHeaders") != "content-length;host" ||
		query.Get("partNumber") != "3" || query.Get("uploadId") != "upload/1+2" || query.Get("X-Amz-Expires") != "900" {
		t.Fatalf("part URL = %s", partURL)
	}
}

func TestUploadLimitsAreEnforcedPerPurpose(t *testing.T) {
	limits := DefaultUploadLimits()

	if limits.Max("library") != 0 {
		t.Fatalf("library limit = %d, want 0", limits.Max("library"))
	}
	if limits.Max(UploadPurposeNoteAttachment) != 15<<20 {
		t.Fatalf("note limit = %d, want 15MB", limits.Max(UploadPurposeNoteAttachment))
	}
	if limits.Max(UploadPurposeChatAttachment) != 10<<20 {
		t.Fatalf("chat limit = %d, want 10MB", limits.Max(UploadPurposeChatAttachment))
	}
	if limits.Max("avatar") != 0 {
		t.Fatalf("unknown purpose limit = %d, want 0", limits.Max("avatar"))
	}
}

func TestUploadLimitsRejectValuesAboveDatabaseCeiling(t *testing.T) {
	limits := DefaultUploadLimits()
	limits.ChatAttachment = db.DefaultChatAttachmentMaxFileBytes + 1

	if err := limits.Validate(); err == nil {
		t.Fatal("validate() accepted a chat limit above the database ceiling")
	}

	limits = DefaultUploadLimits()
	limits.NoteAttachment = 0
	if err := limits.Validate(); err == nil {
		t.Fatal("validate() accepted a zero note limit")
	}
}

func TestUploadPurposeEnabledRejectsJournalAssetsOnGenericEndpoint(t *testing.T) {
	service := &SpaceLibraryService{
		AttachmentsEnabled: true,
		NoteAssetsEnabled:  true,
	}

	if service.UploadPurposeEnabled(UploadPurposeNoteAttachment) {
		t.Fatal("generic Library upload endpoint accepted the note_attachment purpose")
	}
	if !service.UploadPurposeEnabled(UploadPurposeChatAttachment) {
		t.Fatal("the chat attachment purpose should be enabled")
	}
	if service.UploadPurposeEnabled("library") {
		t.Fatal("the retired Library purpose was accepted")
	}
	if service.UploadPurposeEnabled("avatar") {
		t.Fatal("unknown purpose accepted")
	}
}

func TestNoteAssetMIMEAllowlistRejectsActiveAndGenericContent(t *testing.T) {
	for _, allowed := range []string{"image/png", "image/jpeg", "IMAGE/WEBP"} {
		if !SupportedNoteAssetMIME(allowed) {
			t.Fatalf("safe note MIME %q was rejected", allowed)
		}
	}
	for _, rejected := range []string{
		"image/svg+xml",
		"text/html",
		"application/octet-stream",
	} {
		if SupportedNoteAssetMIME(rejected) {
			t.Fatalf("unsafe note MIME %q was accepted", rejected)
		}
	}
}

func TestJournalAssetMIMEAllowlistIsRasterOnly(t *testing.T) {
	for _, rejected := range []string{
		"image/svg+xml",
		"text/html",
		"application/pdf",
		"application/octet-stream",
		"video/mp4",
	} {
		if SupportedNoteAssetMIME(rejected) {
			t.Fatalf("active or non-image Journal MIME %q was accepted", rejected)
		}
	}
}

package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// stubPresigner records what the service asked to sign without contacting R2.
type stubPresigner struct {
	putKey, getKey   string
	putTTL, getTTL   time.Duration
	getFilename      string
	putMetadata      LibraryObjectMetadata
	presignPutCalled bool
}

func (p *stubPresigner) PresignPut(_ context.Context, key string, metadata LibraryObjectMetadata, ttl time.Duration) (PresignedTransfer, error) {
	p.presignPutCalled, p.putKey, p.putMetadata, p.putTTL = true, key, metadata, ttl
	return PresignedTransfer{
		URL:    "https://account.r2.cloudflarestorage.com/misty/" + key + "?X-Amz-Signature=abc",
		Method: http.MethodPut,
		Headers: map[string]string{
			"Content-Type":          metadata.MIMEType,
			"x-amz-checksum-sha256": "checksum",
		},
		ExpiresAt: time.Now().Add(ttl).UTC(),
	}, nil
}

func (p *stubPresigner) PresignGet(_ context.Context, key, filename string, ttl time.Duration) (PresignedDownload, error) {
	p.getKey, p.getFilename, p.getTTL = key, filename, ttl
	return PresignedDownload{
		URL:       "https://account.r2.cloudflarestorage.com/misty/" + key + "?X-Amz-Signature=def",
		ExpiresAt: time.Now().Add(ttl).UTC(),
		Filename:  filename,
	}, nil
}

func directTransferService(t *testing.T, presigner LibraryObjectPresigner) *SpaceLibraryService {
	t.Helper()
	return &SpaceLibraryService{
		TestingStore:        NewMemoryLibraryObjectStore(),
		TestingUploadLimits: DefaultUploadLimits(),
		TestingPresigner:    presigner,
		TestingTransfers:    TransferTTLs{UploadURLTTL: 15 * time.Minute, DownloadURLTTL: 2 * time.Minute},
	}
}

func TestUploadTransferReturnsAbsoluteSignedPutWithoutMistyCredentials(t *testing.T) {
	presigner := &stubPresigner{}
	service := directTransferService(t, presigner)
	digest := sha256.Sum256([]byte("direct upload fixture"))
	upload := &db.LibraryUpload{
		ID: "upload_1", SpaceID: "space_1", ObjectKey: "library/directfixture01",
		RequestedByteSize: 21, ClientSHA256: hex.EncodeToString(digest[:]), ClientDeclaredMIMEType: "text/plain",
	}

	transfer, err := service.TestingUploadTransfer(context.Background(), upload, "misty-upload-token", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	if !presigner.presignPutCalled {
		t.Fatal("uploadTransfer() did not sign an R2 PUT")
	}
	if presigner.putKey != upload.ObjectKey {
		t.Fatalf("signed key = %q, want %q", presigner.putKey, upload.ObjectKey)
	}
	if presigner.putMetadata.ByteSize != upload.RequestedByteSize || presigner.putMetadata.SHA256 != upload.ClientSHA256 {
		t.Fatalf("signed metadata = %#v", presigner.putMetadata)
	}
	if presigner.putTTL != 15*time.Minute {
		t.Fatalf("upload TTL = %s, want 15m", presigner.putTTL)
	}
	// The absolute R2 URL must never carry a Misty upload token: the client
	// sends it with no cookies and no Misty authorization header.
	if _, leaked := transfer.Headers[TestingLibraryUploadTokenHeader]; leaked {
		t.Fatalf("signed transfer leaked the Misty upload token: %#v", transfer.Headers)
	}
	if transfer.URL[:8] != "https://" {
		t.Fatalf("URL = %q, want an absolute https URL", transfer.URL)
	}
}

// A store that cannot sign keeps the relative proxy route, which is what
// makes local development work without R2 credentials.
func TestUploadTransferUsesTheProxyWhenTheStoreCannotSign(t *testing.T) {
	service := &SpaceLibraryService{TestingStore: NewMemoryLibraryObjectStore(), TestingUploadLimits: DefaultUploadLimits()}
	upload := &db.LibraryUpload{ID: "upload_1", SpaceID: "space_1", ObjectKey: "library/proxyfixture001", ClientDeclaredMIMEType: "text/plain"}

	transfer, err := service.TestingUploadTransfer(context.Background(), upload, "misty-upload-token", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	if transfer.URL != "/spaces/space_1/library/uploads/upload_1/content" {
		t.Fatalf("URL = %q, want the relative proxy route", transfer.URL)
	}
	if transfer.Headers[TestingLibraryUploadTokenHeader] != "misty-upload-token" {
		t.Fatal("proxy transfer must carry the Misty upload token")
	}
}

func TestWriteDownloadReturnsSignedDescriptorInsteadOfBytes(t *testing.T) {
	presigner := &stubPresigner{}
	service := directTransferService(t, presigner)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/spaces/space_1/library/items/item_1/download", nil)
	download := &db.LibraryDownload{
		ObjectKey: "library/downloadfixture1", Filename: "quarterly report.pdf",
		MIMEType: "application/pdf", ByteSize: 4096, SHA256: "0",
	}

	service.TestingWriteDownload(recorder, request, download)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var descriptor PresignedDownload
	if err := json.Unmarshal(recorder.Body.Bytes(), &descriptor); err != nil {
		t.Fatalf("response was not a signed descriptor: %v (%s)", err, recorder.Body.String())
	}
	if descriptor.URL == "" || descriptor.Filename != "quarterly report.pdf" {
		t.Fatalf("descriptor = %#v", descriptor)
	}
	if presigner.getKey != download.ObjectKey {
		t.Fatalf("signed key = %q, want %q", presigner.getKey, download.ObjectKey)
	}
	if presigner.getTTL != 2*time.Minute {
		t.Fatalf("download TTL = %s, want 2m", presigner.getTTL)
	}
	if descriptor.ExpiresAt.IsZero() || !descriptor.ExpiresAt.After(time.Now()) {
		t.Fatalf("ExpiresAt = %s, want a future expiry", descriptor.ExpiresAt)
	}
}

func TestJournalAssetDownloadReturnsVerifiedR2Descriptor(t *testing.T) {
	presigner := &stubPresigner{}
	service := directTransferService(t, presigner)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		"/spaces/space_1/drawings/drawing_1/assets/asset_1/download",
		nil,
	)
	download := &db.LibraryDownload{
		ObjectKey: "library/drawingfixture01",
		Filename:  "drawing.png",
		MIMEType:  "image/png",
		ByteSize:  2048,
		SHA256:    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}

	service.TestingWriteJournalAssetDownload(recorder, request, download)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var descriptor PresignedDownload
	if err := json.Unmarshal(recorder.Body.Bytes(), &descriptor); err != nil {
		t.Fatal(err)
	}
	if descriptor.URL == "" ||
		descriptor.MIMEType != download.MIMEType ||
		descriptor.ByteSize != download.ByteSize ||
		descriptor.SHA256 != download.SHA256 {
		t.Fatalf("descriptor = %#v", descriptor)
	}
	if recorder.Header().Get(TestingLibrarySignedDownloadHeader) != "1" {
		t.Fatal("signed download marker is missing")
	}
}

func TestJournalAssetDownloadNeverFallsBackToServerBytes(t *testing.T) {
	service := &SpaceLibraryService{
		TestingStore: NewMemoryLibraryObjectStore(),
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		"/spaces/space_1/notes/note_1/assets/asset_1/download",
		nil,
	)

	service.TestingWriteJournalAssetDownload(
		recorder,
		request,
		&db.LibraryDownload{
			ObjectKey: "library/notefixture01",
			Filename:  "note.png",
			MIMEType:  "image/png",
			ByteSize:  10,
			SHA256:    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
	)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "journal_asset_direct_transfer_required") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

func TestJournalAssetPurposesNeverUseTheProxy(t *testing.T) {
	for _, purpose := range []UploadPurpose{
		UploadPurposeNoteAttachment,
		UploadPurposeDrawingAsset,
	} {
		if !TestingIsJournalAssetPurpose(purpose) {
			t.Fatalf("%q was not recognized as a Journal asset purpose", purpose)
		}
	}
	if TestingIsJournalAssetPurpose(UploadPurposeLibrary) {
		t.Fatal("Library upload was treated as a Journal asset")
	}
}

// Direct transfer has no off switch: it is on exactly when the store can sign.
// A development store that cannot sign transparently keeps the proxy route,
// which is what lets local development work without R2 credentials.
func TestDirectTransferFollowsStoreCapability(t *testing.T) {
	signing, err := NewSpaceLibraryService(&db.Database{}, &S3LibraryObjectStore{TestingBucket: "misty", TestingPresigner: &recordingPresigner{}}, true, DefaultUploadLimits())
	if err != nil {
		t.Fatal(err)
	}
	if !signing.TestingDirectTransfersActive() {
		t.Fatal("a signing store did not enable direct transfer")
	}

	local, err := NewSpaceLibraryService(&db.Database{}, NewMemoryLibraryObjectStore(), true, DefaultUploadLimits())
	if err != nil {
		t.Fatal(err)
	}
	if local.TestingDirectTransfersActive() {
		t.Fatal("a store that cannot sign reported direct transfer as active")
	}
}

// The TTLs remain tunable, but an unbounded lifetime is still rejected, since
// a long-lived signed URL is effectively a public one.
func TestTransferTTLsMustStayBounded(t *testing.T) {
	service := &SpaceLibraryService{TestingStore: NewMemoryLibraryObjectStore(), TestingUploadLimits: DefaultUploadLimits()}

	if err := service.TestingConfigureTransfers(TransferTTLs{UploadURLTTL: 48 * time.Hour, DownloadURLTTL: 2 * time.Minute}); err == nil {
		t.Fatal("configureTransfers() accepted a 48h upload URL lifetime")
	}
	if err := service.TestingConfigureTransfers(DefaultTransferTTLs()); err != nil {
		t.Fatalf("configureTransfers(defaults) = %v", err)
	}
}

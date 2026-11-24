package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/misty-org/misty-server/internal/platform/httpapi"
)

type fakeS3Object struct {
	body        []byte
	contentType string
	sha256      string
	checksum    string
	modified    time.Time
}

type fakeS3Request struct {
	method string
	path   string
	query  url.Values
	header http.Header
}

type fakeS3CompletedPart struct {
	PartNumber int
	ETag       string
}

type fakeS3Server struct {
	mu        sync.Mutex
	objects   map[string]fakeS3Object
	requests  []fakeS3Request
	completed []fakeS3CompletedPart
}

type fakeS3ListEntry struct {
	Key          string
	LastModified string
	ETag         string
	Size         int64
	StorageClass string
}

type fakeS3ListResult struct {
	XMLName               xml.Name `xml:"http://s3.amazonaws.com/doc/2006-03-01/ ListBucketResult"`
	Name                  string
	Prefix                string
	KeyCount              int
	MaxKeys               int
	IsTruncated           bool
	NextContinuationToken string `xml:",omitempty"`
	Contents              []fakeS3ListEntry
}

func newFakeS3Store(t *testing.T) (*fakeS3Server, *S3LibraryObjectStore) {
	t.Helper()
	fake := &fakeS3Server{objects: map[string]fakeS3Object{}}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	store, err := NewS3LibraryObjectStore(S3LibraryObjectStoreConfig{
		Endpoint: server.URL, Region: "auto", Bucket: "misty",
		AccessKeyID: "access", SecretAccessKey: "secret",
		ForcePathStyle: true, BucketPrivate: true, PermanentObjects: true, AllowInsecureLocal: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return fake, store
}

func (f *fakeS3Server) put(key string, object fakeS3Object) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[key] = object
}

func (f *fakeS3Server) last(method string) fakeS3Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.requests) - 1; i >= 0; i-- {
		if f.requests[i].method == method {
			return f.requests[i]
		}
	}
	return fakeS3Request{}
}

func (f *fakeS3Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	query := r.URL.Query()
	f.requests = append(f.requests, fakeS3Request{method: r.Method, path: r.URL.Path, query: query, header: r.Header.Clone()})
	if r.URL.Path == "/misty" || r.URL.Path == "/misty/" {
		if r.Method == http.MethodGet {
			f.list(w, query)
			return
		}
		w.WriteHeader(http.StatusOK)
		return
	}
	key := strings.TrimPrefix(r.URL.Path, "/misty/")
	switch {
	case r.Method == http.MethodPost && query.Has("uploads"):
		fmt.Fprintf(w, "<InitiateMultipartUploadResult><Bucket>misty</Bucket><Key>%s</Key><UploadId>upload-1</UploadId></InitiateMultipartUploadResult>", key)
	case r.Method == http.MethodPost && query.Get("uploadId") != "":
		var complete struct {
			Parts []fakeS3CompletedPart `xml:"Part"`
		}
		if err := xml.NewDecoder(r.Body).Decode(&complete); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f.completed = complete.Parts
		fmt.Fprintf(w, `<CompleteMultipartUploadResult><Location>http://fake/misty/%s</Location><Bucket>misty</Bucket><Key>%s</Key><ETag>"done"</ETag></CompleteMultipartUploadResult>`, key, key)
	case r.Method == http.MethodDelete && query.Get("uploadId") != "":
		w.WriteHeader(http.StatusNotFound)
	case r.Method == http.MethodPut:
		body, _ := io.ReadAll(r.Body)
		f.objects[key] = fakeS3Object{
			body: body, contentType: r.Header.Get("Content-Type"),
			sha256: r.Header.Get("X-Amz-Meta-" + LibrarySHA256MetadataKey), checksum: r.Header.Get("X-Amz-Checksum-Sha256"),
			modified: time.Now().UTC(),
		}
		w.Header().Set("ETag", `"stored"`)
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodDelete:
		delete(f.objects, key)
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodHead || r.Method == http.MethodGet:
		object, ok := f.objects[key]
		if !ok {
			if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(http.StatusNotFound)
				fmt.Fprint(w, "<Error><Code>NoSuchKey</Code><Message>missing</Message></Error>")
				return
			}
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(object.body)))
		w.Header().Set("Content-Type", object.contentType)
		w.Header().Set("Last-Modified", object.modified.Format(http.TimeFormat))
		w.Header().Set("ETag", `"stored"`)
		w.Header().Set("X-Amz-Meta-"+LibrarySHA256MetadataKey, object.sha256)
		if r.Header.Get("X-Amz-Checksum-Mode") == "ENABLED" && object.checksum != "" {
			w.Header().Set("X-Amz-Checksum-Sha256", object.checksum)
		}
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write(object.body)
		}
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (f *fakeS3Server) list(w http.ResponseWriter, query url.Values) {
	prefix := query.Get("prefix")
	after := query.Get("start-after")
	if token := query.Get("continuation-token"); token != "" {
		after = token
	}
	maxKeys, _ := strconv.Atoi(query.Get("max-keys"))
	keys := make([]string, 0, len(f.objects))
	for key := range f.objects {
		if strings.HasPrefix(key, prefix) && key > after {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	result := fakeS3ListResult{Name: "misty", Prefix: prefix, MaxKeys: maxKeys}
	if maxKeys > 0 && len(keys) > maxKeys {
		keys = keys[:maxKeys]
		result.IsTruncated = true
		result.NextContinuationToken = keys[len(keys)-1]
	}
	for _, key := range keys {
		object := f.objects[key]
		result.Contents = append(result.Contents, fakeS3ListEntry{
			Key: key, LastModified: object.modified.Format("2006-01-02T15:04:05.000Z"),
			ETag: `"stored"`, Size: int64(len(object.body)), StorageClass: "STANDARD",
		})
	}
	result.KeyCount = len(result.Contents)
	w.Header().Set("Content-Type", "application/xml")
	_ = xml.NewEncoder(w).Encode(result)
}

func fakeS3Fixture(body []byte) (LibraryObjectMetadata, string) {
	digest := sha256.Sum256(body)
	return LibraryObjectMetadata{ByteSize: int64(len(body)), SHA256: hex.EncodeToString(digest[:]), MIMEType: "text/plain"},
		base64.StdEncoding.EncodeToString(digest[:])
}

func TestS3StorePutSendsTheChecksumAndMetadataThenReadsThemBack(t *testing.T) {
	fake, store := newFakeS3Store(t)
	body := []byte("library object body")
	metadata, checksum := fakeS3Fixture(body)

	if err := store.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), "library/storefixture01", bytes.NewReader(body), metadata); err != nil {
		t.Fatal(err)
	}

	put := fake.last(http.MethodPut)
	if put.path != "/misty/library/storefixture01" {
		t.Fatalf("PUT path = %q, want path-style", put.path)
	}
	if put.header.Get("X-Amz-Checksum-Sha256") != checksum ||
		put.header.Get("X-Amz-Meta-"+LibrarySHA256MetadataKey) != metadata.SHA256 ||
		put.header.Get("Content-Type") != metadata.MIMEType ||
		put.header.Get("X-Amz-Content-Sha256") != "UNSIGNED-PAYLOAD" {
		t.Fatalf("PUT headers = %#v", put.header)
	}
	if !strings.Contains(put.header.Get("Authorization"), "Credential=access/") ||
		!strings.Contains(put.header.Get("Authorization"), "/auto/s3/aws4_request") {
		t.Fatalf("Authorization = %q", put.header.Get("Authorization"))
	}

	head, err := store.Head(context.Background(), "library/storefixture01")
	if err != nil {
		t.Fatal(err)
	}
	if head != metadata {
		t.Fatalf("Head() = %#v, want %#v", head, metadata)
	}

	reader, opened, err := store.Open(context.Background(), "library/storefixture01")
	if err != nil {
		t.Fatal(err)
	}
	read, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(read, body) || opened != metadata {
		t.Fatalf("Open() = %q, %#v", read, opened)
	}
	if fake.last(http.MethodGet).header.Get("X-Amz-Checksum-Mode") != "ENABLED" {
		t.Fatal("Open() did not ask for the object's checksum")
	}
}

func TestS3StoreOpenFailsWhenTheBodyDoesNotMatchItsChecksum(t *testing.T) {
	fake, store := newFakeS3Store(t)
	metadata, _ := fakeS3Fixture([]byte("original body"))
	_, otherChecksum := fakeS3Fixture([]byte("another body"))
	fake.put("library/storefixture02", fakeS3Object{
		body: []byte("original body"), contentType: metadata.MIMEType, sha256: metadata.SHA256,
		checksum: otherChecksum, modified: time.Now().UTC(),
	})

	reader, _, err := store.Open(context.Background(), "library/storefixture02")
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := io.ReadAll(reader); err == nil || !strings.Contains(err.Error(), "checksum did not match") {
		t.Fatalf("ReadAll() error = %v, want a checksum mismatch", err)
	}
}

func TestS3StoreMapsMissingObjectsAndUploadsToNotFound(t *testing.T) {
	_, store := newFakeS3Store(t)

	if _, err := store.Head(context.Background(), "library/missingfixture"); !errors.Is(err, ErrLibraryObjectNotFound) {
		t.Fatalf("Head() error = %v, want not found", err)
	}
	if _, _, err := store.Open(context.Background(), "library/missingfixture"); !errors.Is(err, ErrLibraryObjectNotFound) {
		t.Fatalf("Open() error = %v, want not found", err)
	}
	if err := store.AbortMultipartUpload(context.Background(), "library/missingfixture", "upload-1"); !errors.Is(err, ErrLibraryObjectNotFound) {
		t.Fatalf("AbortMultipartUpload() error = %v, want not found", err)
	}
	if err := store.Delete(context.Background(), "library/missingfixture"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
}

func TestS3StoreListPagesByLimitAndResumesFromTheCursor(t *testing.T) {
	fake, store := newFakeS3Store(t)
	modified := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, key := range []string{"library/inventory0001", "library/inventory0002", "library/inventory0003", "avatars/inventory0004"} {
		fake.put(key, fakeS3Object{body: []byte(key), modified: modified})
	}

	first, err := store.List(context.Background(), "library/", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Objects) != 2 || first.Objects[0].Key != "library/inventory0001" || first.NextCursor != "library/inventory0002" {
		t.Fatalf("first page = %#v", first)
	}
	if first.Objects[0].ByteSize != int64(len("library/inventory0001")) || !first.Objects[0].LastModified.Equal(modified) {
		t.Fatalf("first entry = %#v", first.Objects[0])
	}
	request := fake.last(http.MethodGet)
	if request.query.Get("list-type") != "2" || request.query.Get("prefix") != "library/" ||
		request.query.Get("max-keys") != "3" || request.query.Has("fetch-owner") {
		t.Fatalf("list query = %#v", request.query)
	}

	second, err := store.List(context.Background(), "library/", first.NextCursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Objects) != 1 || second.Objects[0].Key != "library/inventory0003" || second.NextCursor != "" {
		t.Fatalf("second page = %#v", second)
	}
}

func TestS3StoreMultipartKeepsMetadataAndSortsParts(t *testing.T) {
	fake, store := newFakeS3Store(t)
	metadata, _ := fakeS3Fixture([]byte("multipart body"))

	uploadID, err := store.CreateMultipartUpload(context.Background(), "library/multipart0001", metadata)
	if err != nil {
		t.Fatal(err)
	}
	if uploadID != "upload-1" {
		t.Fatalf("upload ID = %q", uploadID)
	}
	create := fake.last(http.MethodPost)
	if create.header.Get("Content-Type") != metadata.MIMEType ||
		create.header.Get("X-Amz-Meta-"+LibrarySHA256MetadataKey) != metadata.SHA256 {
		t.Fatalf("create headers = %#v", create.header)
	}

	if err := store.CompleteMultipartUpload(context.Background(), "library/multipart0001", uploadID, []LibraryUploadPart{
		{Number: 2, ETag: `"second"`}, {Number: 1, ETag: `"first"`},
	}); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	completed := fake.completed
	fake.mu.Unlock()
	if len(completed) != 2 || completed[0] != (fakeS3CompletedPart{PartNumber: 1, ETag: `"first"`}) ||
		completed[1] != (fakeS3CompletedPart{PartNumber: 2, ETag: `"second"`}) {
		t.Fatalf("completed parts = %#v", completed)
	}
	if fake.last(http.MethodPost).query.Get("uploadId") != uploadID {
		t.Fatal("completion did not name the upload")
	}
}

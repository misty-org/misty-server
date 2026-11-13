package console

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	. "github.com/misty-org/misty-server/internal/console"
)

func exportManifest(t *testing.T, base, filename, checksum string, size int) AccountExportManifest {
	t.Helper()
	answer := fmt.Sprintf(`{
		"account_data": {"account": {"email": "someone@example.com"}},
		"documents": [{"kind": "note", "id": "note-1", "title": "Plan", "download_url": %q}],
		"assets": [{"kind": "library", "id": "file-1", "filename": %q, "byte_size": %d, "sha256": %q, "download": {"url": %q}}]
	}`, base+"/note", filename, size, checksum, base+"/file")
	var manifest AccountExportManifest
	if err := json.Unmarshal([]byte(answer), &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func exportDownloads(t *testing.T, file string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/note":
			_, _ = w.Write([]byte("yjs-bytes"))
		case "/file":
			_, _ = w.Write([]byte(file))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestAccountArchiveHoldsTheFilesAndNoLinks(t *testing.T) {
	file := "file contents"
	sum := sha256.Sum256([]byte(file))
	downloads := exportDownloads(t, file)
	manifest := exportManifest(t, downloads.URL, "../../etc/pass wd.txt", hex.EncodeToString(sum[:]), len(file))

	var out bytes.Buffer
	if err := WriteAccountArchive(context.Background(), &out, manifest, downloads.Client(), time.Now()); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, entry := range archive.File {
		reader, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(reader)
		_ = reader.Close()
		files[entry.Name] = string(data)
	}
	if files["journal/notes/note-1.yjs"] != "yjs-bytes" {
		t.Fatalf("note missing: %v", files)
	}
	// A filename can't climb out of its folder.
	if files["assets/librarys/file-1-pass_wd.txt"] != file {
		t.Fatalf("file missing or unsafe: %v", files)
	}
	summary := files["manifest.json"]
	if !strings.Contains(summary, "someone@example.com") || strings.Contains(summary, downloads.URL) {
		t.Fatalf("the manifest should hold the data and no links: %s", summary)
	}
}

func TestAccountArchiveStopsOnAFileThatDoesNotMatch(t *testing.T) {
	file := "file contents"
	sum := sha256.Sum256([]byte(file))
	checksum := hex.EncodeToString(sum[:])
	for name, manifest := range map[string]func(string) AccountExportManifest{
		"size": func(base string) AccountExportManifest {
			return exportManifest(t, base, "a.txt", checksum, len(file)+1)
		},
		"checksum": func(base string) AccountExportManifest {
			return exportManifest(t, base, "a.txt", strings.Repeat("0", 64), len(file))
		},
	} {
		downloads := exportDownloads(t, file)
		err := WriteAccountArchive(context.Background(), io.Discard, manifest(downloads.URL), downloads.Client(), time.Now())
		if err == nil {
			t.Fatalf("%s mismatch should stop the archive", name)
		}
	}
}

func TestAccountArchiveStopsOnAFailedDownload(t *testing.T) {
	downloads := exportDownloads(t, "x")
	manifest := exportManifest(t, downloads.URL+"/missing", "a.txt", "", 1)
	if err := WriteAccountArchive(context.Background(), io.Discard, manifest, downloads.Client(), time.Now()); err == nil {
		t.Fatal("a failed download should stop the archive")
	}
}

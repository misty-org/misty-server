package console

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

type AccountExportManifest struct {
	AccountData json.RawMessage         `json:"account_data"`
	Documents   []AccountExportDocument `json:"documents"`
	Assets      []AccountExportAsset    `json:"assets"`
}

type AccountExportDocument struct {
	accountDocumentRecord
	DownloadURL string `json:"download_url"`
}

type AccountExportAsset struct {
	accountAssetRecord
	Download struct {
		URL string `json:"url"`
	} `json:"download"`
}

type accountDocumentRecord struct {
	Kind       string `json:"kind"`
	ID         string `json:"id"`
	SpaceID    string `json:"space_id"`
	Title      string `json:"title"`
	ACLVersion int64  `json:"acl_version"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

type accountAssetRecord struct {
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	ParentID  string `json:"parent_id"`
	Filename  string `json:"filename"`
	MIMEType  string `json:"mime_type"`
	ByteSize  int64  `json:"byte_size"`
	SHA256    string `json:"sha256"`
	CreatedAt string `json:"created_at"`
}

func (s *Server) exportCustomer(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.NotFound(w, r)
		return
	}
	id := chi.URLParam(r, "id")
	account, err := s.db.OperatorAccountByID(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	manifest, err := s.operator.exportAccount(r.Context(), id)
	if err != nil {
		view := s.customersViewFor(r, id)
		if view.Detail != nil {
			view.Detail.Err = "Couldn't export this account: " + err.Error()
		}
		render(w, r, layout(layoutView{
			Title:   "Customers",
			Active:  "customers",
			Nav:     navigation(s.cfg),
			CSRF:    sessionFrom(r.Context()).csrf,
			Content: customersPage(view),
		}))
		return
	}
	s.audit(r, "Exported data for", account.Email, "")
	now := time.Now().UTC()
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", fmt.Sprintf(
		`attachment; filename="misty-account-%s-%s.zip"`, exportSegment(account.Email), now.Format("2006-01-02"),
	))
	if err := WriteAccountArchive(r.Context(), w, manifest, &http.Client{}, now); err != nil {
		log.Printf("console: export of %s stopped: %v", id, err)
		panic(http.ErrAbortHandler)
	}
}

func WriteAccountArchive(ctx context.Context, w io.Writer, manifest AccountExportManifest, client *http.Client, now time.Time) error {
	archive := zip.NewWriter(w)
	documents := make([]accountDocumentRecord, 0, len(manifest.Documents))
	for _, document := range manifest.Documents {
		documents = append(documents, document.accountDocumentRecord)
	}
	assets := make([]accountAssetRecord, 0, len(manifest.Assets))
	for _, asset := range manifest.Assets {
		assets = append(assets, asset.accountAssetRecord)
	}
	summary, err := json.MarshalIndent(map[string]any{
		"format_version": 1,
		"generated_at":   now.Format(time.RFC3339),
		"account_data":   manifest.AccountData,
		"documents":      documents,
		"assets":         assets,
		"journal_format": "Raw Yjs update; apply with Y.applyUpdate to an empty Y.Doc.",
	}, "", "  ")
	if err != nil {
		return err
	}
	file, err := archive.Create("manifest.json")
	if err != nil {
		return err
	}
	if _, err := file.Write(append(summary, '\n')); err != nil {
		return err
	}
	for _, document := range manifest.Documents {
		name := "journal/" + exportSegment(document.Kind) + "s/" + exportSegment(document.ID) + ".yjs"
		if _, _, err := addExportFile(ctx, archive, client, name, document.DownloadURL); err != nil {
			return fmt.Errorf("%s %s: %w", document.Kind, document.ID, err)
		}
	}
	for _, asset := range manifest.Assets {
		name := "assets/" + exportSegment(asset.Kind) + "s/" + exportSegment(asset.ID) + "-" + exportSegment(asset.Filename)
		size, checksum, err := addExportFile(ctx, archive, client, name, asset.Download.URL)
		if err != nil {
			return fmt.Errorf("file %s: %w", asset.ID, err)
		}
		if size != asset.ByteSize {
			return fmt.Errorf("file %s is %d bytes, expected %d", asset.ID, size, asset.ByteSize)
		}
		if checksum != strings.ToLower(asset.SHA256) {
			return fmt.Errorf("file %s did not match its checksum", asset.ID)
		}
	}
	return archive.Close()
}

func addExportFile(ctx context.Context, archive *zip.Writer, client *http.Client, name, link string) (int64, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, http.NoBody)
	if err != nil {
		return 0, "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, "", fmt.Errorf("download answered %s", resp.Status)
	}
	file, err := archive.Create(name)
	if err != nil {
		return 0, "", err
	}
	digest := sha256.New()
	size, err := io.Copy(io.MultiWriter(file, digest), resp.Body)
	if err != nil {
		return 0, "", err
	}
	return size, hex.EncodeToString(digest.Sum(nil)), nil
}

var exportUnsafeCharacters = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func exportSegment(value string) string {
	if index := strings.LastIndexAny(value, `/\`); index >= 0 {
		value = value[index+1:]
	}
	value = strings.TrimLeft(exportUnsafeCharacters.ReplaceAllString(value, "_"), ".")
	if len(value) > 120 {
		value = value[:120]
	}
	if value == "" {
		return "item"
	}
	return value
}

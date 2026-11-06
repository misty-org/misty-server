package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"strconv"
	"strings"

	"github.com/misty-org/misty-server/internal/platform/db"

	"github.com/ledongthuc/pdf"
	"github.com/misty-org/misty-server/internal/workflow"
)

type docxTextNode struct {
	Text string `xml:",chardata"`
}

func extractDOCXText(data []byte) (string, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	for _, file := range reader.File {
		if file.Name != "word/document.xml" {
			continue
		}
		stream, err := file.Open()
		if err != nil {
			return "", err
		}
		decoder := xml.NewDecoder(io.LimitReader(stream, 25_000_001))
		var builder strings.Builder
		for {
			token, err := decoder.Token()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				stream.Close()
				return "", err
			}
			switch value := token.(type) {
			case xml.StartElement:
				if value.Name.Local == "t" {
					var node docxTextNode
					if err := decoder.DecodeElement(&node, &value); err != nil {
						stream.Close()
						return "", err
					}
					builder.WriteString(node.Text)
				} else if value.Name.Local == "p" || value.Name.Local == "br" {
					builder.WriteByte('\n')
				}
			}
		}
		stream.Close()
		return strings.TrimSpace(builder.String()), nil
	}
	return "", workflow.ErrUnsupportedContent
}

func TestingExtractDOCXText(data []byte) (string, error) {
	return extractDOCXText(data)
}

// Attachments are at most this big, and their text no bigger.
const maxPDFTextBytes = 25_000_000

func extractPDFText(data []byte) (string, error) {
	return extractPDFTextLimited(data, maxPDFTextBytes)
}

// Some fonts decode spaces to NUL, which text columns and tools refuse.
func cleanPDFText(content string) string {
	return strings.ReplaceAll(content, "\x00", " ")
}

func extractPDFTextLimited(data []byte, limit int) (text string, err error) {
	// The parser panics on some malformed files, and these come from anyone in a Space.
	defer func() {
		if recover() != nil {
			text, err = "", workflow.ErrUnsupportedContent
		}
	}()
	reader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	var builder strings.Builder
	for number := 1; number <= reader.NumPage(); number++ {
		page := reader.Page(number)
		if page.V.IsNull() {
			continue
		}
		content, err := page.GetPlainText(nil)
		if err != nil {
			return "", err
		}
		content = cleanPDFText(content)
		if strings.TrimSpace(content) == "" {
			continue
		}
		section := "\n[Page " + strconv.Itoa(number) + "]\n" + content
		if builder.Len()+len(section) > limit {
			builder.WriteString(section[:max(limit-builder.Len(), 0)])
			break
		}
		builder.WriteString(section)
	}
	if strings.TrimSpace(builder.String()) == "" {
		return "", workflow.ErrUnsupportedContent
	}
	return strings.TrimSpace(builder.String()), nil
}

func (s *SpaceLibraryService) ReadExplicitAgentAttachment(ctx context.Context, userID, spaceID, kind, resourceID string, maximumBytes int64) ([]byte, *db.LibraryDownload, error) {
	if maximumBytes < 1 || maximumBytes > 25_000_000 {
		maximumBytes = 25_000_000
	}
	var download *db.LibraryDownload
	var err error
	switch kind {
	case "library_item":
		download, err = s.database.ExplicitAgentLibraryItemDownload(ctx, userID, spaceID, resourceID)
	case "chat_attachment":
		download, err = s.database.MessageAttachmentDownload(ctx, userID, spaceID, resourceID)
	default:
		return nil, nil, db.ErrSpaceInvalid
	}
	if err != nil {
		return nil, nil, err
	}
	if download.ByteSize > maximumBytes {
		return nil, download, db.ErrLibraryInvalid
	}
	stream, metadata, err := s.Store.Open(ctx, download.ObjectKey)
	if err != nil {
		return nil, download, err
	}
	defer stream.Close()
	if metadata.ByteSize != download.ByteSize || metadata.SHA256 != download.SHA256 {
		return nil, download, db.ErrLibraryUploadMismatch
	}
	data, err := io.ReadAll(io.LimitReader(stream, maximumBytes+1))
	if err != nil || int64(len(data)) > maximumBytes {
		if err == nil {
			err = db.ErrLibraryInvalid
		}
		return nil, download, err
	}
	mimeType := strings.ToLower(strings.TrimSpace(strings.Split(download.MIMEType, ";")[0]))
	textual := strings.HasPrefix(mimeType, "text/") || mimeType == "application/json" || mimeType == "application/xml" || mimeType == "application/x-ndjson" || mimeType == "application/yaml" || mimeType == "text/csv"
	if textual {
		return data, download, nil
	}
	if mimeType == "application/vnd.openxmlformats-officedocument.wordprocessingml.document" || strings.HasSuffix(strings.ToLower(download.Filename), ".docx") {
		text, err := extractDOCXText(data)
		return []byte(text), download, err
	}
	if mimeType == "application/pdf" || strings.HasSuffix(strings.ToLower(download.Filename), ".pdf") {
		text, err := extractPDFText(data)
		return []byte(text), download, err
	}
	return nil, download, workflow.ErrUnsupportedContent
}

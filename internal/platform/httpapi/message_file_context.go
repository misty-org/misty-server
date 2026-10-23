package api

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kannachi323/misty/server/internal/platform/db"
	"github.com/kannachi323/misty/server/internal/workflow"
)

// explicitAttachmentRef names one file a person attached to a message: a chat
// upload or a Library item.
type explicitAttachmentRef struct {
	Kind        string `json:"kind"`
	ResourceID  string `json:"resource_id"`
	DisplayName string `json:"display_name,omitempty"`
	Version     any    `json:"version,omitempty"`
}

// explicitAttachmentContext reads the attached files for an agent's prompt,
// within a shared budget, and says which ones it couldn't read and why.
func (s *SpacesService) explicitAttachmentContext(ctx context.Context, userID, spaceID string, refs []explicitAttachmentRef) (string, string, []workflow.ContentRef) {
	if s.library == nil {
		return "", "Attached files were not read because attached-file access is unavailable.", nil
	}
	var content, warnings strings.Builder
	sources := []workflow.ContentRef{}
	remaining := 20_000
	for _, ref := range refs {
		if remaining <= 0 {
			warnings.WriteString("\n- Additional attached content was truncated.")
			break
		}
		data, download, err := s.library.ReadExplicitAgentAttachment(ctx, userID, spaceID, ref.Kind, ref.ResourceID, 25_000_000)
		if err != nil {
			name := ref.DisplayName
			if download != nil && download.Filename != "" {
				name = download.Filename
			}
			if name == "" {
				name = ref.ResourceID
			}
			warnings.WriteString("\n- " + name + ": " + explicitAttachmentError(err))
			continue
		}
		name := download.Filename
		text := string(data)
		if len(text) > remaining {
			text = text[:remaining]
			warnings.WriteString("\n- " + name + ": content was truncated to the Agent context limit.")
		}
		remaining -= len(text)
		content.WriteString("\n\n[Explicitly attached file: " + name + "]\n" + text)
		sources = append(sources, workflow.ContentRef{SourceKind: ref.Kind, ProviderID: "misty", ResourceID: ref.ResourceID, Version: explicitSourceVersion(ref.Version), Fingerprint: download.SHA256, DisplayName: name, MIMEType: download.MIMEType, PermissionScope: "message:" + spaceID})
	}
	return content.String(), warnings.String(), sources
}

func explicitSourceVersion(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func explicitAttachmentError(err error) string {
	if errors.Is(err, workflow.ErrUnsupportedContent) {
		return "unsupported file type; images, audio, and video are not read in this beta"
	}
	if errors.Is(err, db.ErrLibraryForbidden) || errors.Is(err, db.ErrSpaceForbidden) {
		return "access was denied"
	}
	if errors.Is(err, db.ErrLibraryNotFound) {
		return "file is missing or no longer available"
	}
	return "file could not be read"
}

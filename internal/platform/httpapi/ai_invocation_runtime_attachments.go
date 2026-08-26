package api

import (
	"context"
	"encoding/base64"
	"errors"
	"io"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func (s *SpacesService) aiInvocationModelAttachments(ctx context.Context, record *db.AIInvocationRecord) ([]map[string]any, error) {
	items := []map[string]any{}
	if record == nil || s.library == nil || s.library.TestingStore == nil {
		return items, nil
	}
	attachments, err := s.database.AIConversationAttachmentsForInvocation(ctx, record.UserID, record.ID)
	if err != nil {
		return nil, err
	}
	for _, attachment := range attachments {
		reader, _, openErr := s.library.TestingStore.Open(ctx, attachment.ModelObjectKey)
		if openErr != nil {
			return nil, openErr
		}
		data, readErr := io.ReadAll(io.LimitReader(reader, (1<<20)+1))
		closeErr := reader.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if len(data) > 1<<20 {
			return nil, errors.New("model image rendition exceeds 1 MB")
		}
		items = append(items, map[string]any{
			"id":           attachment.ID,
			"name":         attachment.DisplayName,
			"mime_type":    attachment.ModelMIMEType,
			"data_url":     "data:" + attachment.ModelMIMEType + ";base64," + base64.StdEncoding.EncodeToString(data),
			"width":        attachment.ModelWidth,
			"height":       attachment.ModelHeight,
			"content_hash": attachment.ModelSHA256,
		})
	}
	return items, nil
}

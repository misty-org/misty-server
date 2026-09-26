package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func (s *SpacesService) aiInvocationModelAttachments(ctx context.Context, record *db.AIInvocationRecord) ([]map[string]any, error) {
	items := []map[string]any{}
	if record == nil || s.library == nil || s.library.TestingStore == nil {
		return items, nil
	}
	var input aiInvocationInput
	_ = json.Unmarshal(record.RequestPayload, &input)
	attachments, err := s.database.AIConversationAttachmentsForInvocation(ctx, record.UserID, record.ID, input.AgentID != "")
	if err != nil {
		return nil, err
	}
	var total int64
	for _, attachment := range attachments {
		if total+attachment.ModelByteSize > 30*1024*1024 {
			continue
		}
		total += attachment.ModelByteSize
		reader, _, openErr := s.library.TestingStore.Open(ctx, attachment.ModelObjectKey)
		if openErr != nil {
			return nil, openErr
		}
		data, readErr := io.ReadAll(io.LimitReader(reader, mistyModelAttachmentLimit(attachment.ModelMIMEType)+1))
		closeErr := reader.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if int64(len(data)) > mistyModelAttachmentLimit(attachment.ModelMIMEType) {
			return nil, errors.New("model attachment exceeds its size limit")
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

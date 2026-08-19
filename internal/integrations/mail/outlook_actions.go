package mail

import (
	"context"
	"errors"
	"net/http"
	stdmail "net/mail"
	"net/url"
	"strings"
)

type graphFileAttachment struct {
	ODataType    string `json:"@odata.type"`
	Name         string `json:"name"`
	ContentType  string `json:"contentType,omitempty"`
	ContentBytes []byte `json:"contentBytes"`
	IsInline     bool   `json:"isInline,omitempty"`
	ContentID    string `json:"contentId,omitempty"`
}

type graphDraftWrite struct {
	Subject       string           `json:"subject"`
	Body          graphItemBody    `json:"body"`
	ToRecipients  []graphRecipient `json:"toRecipients,omitempty"`
	CcRecipients  []graphRecipient `json:"ccRecipients,omitempty"`
	BccRecipients []graphRecipient `json:"bccRecipients,omitempty"`
	ReplyTo       []graphRecipient `json:"replyTo,omitempty"`
}

func (o *Outlook) ModifyThread(ctx context.Context, conversationID string, changes ThreadChanges) (ThreadChangeResult, error) {
	if strings.TrimSpace(conversationID) == "" || (changes.Read == nil && changes.Archived == nil && changes.Starred == nil) {
		return ThreadChangeResult{}, ErrInvalidInput
	}
	query := o.messageQuery(false)
	query.Set("$filter", "conversationId eq "+odataString(conversationID))
	query.Del("$orderby")
	messages, err := o.readAllMessages(ctx, query)
	if err != nil {
		return ThreadChangeResult{}, err
	}
	if len(messages) == 0 {
		return ThreadChangeResult{}, &ProviderError{StatusCode: http.StatusNotFound, Message: "conversation not found"}
	}
	for _, message := range messages {
		patch := make(map[string]any)
		if changes.Read != nil {
			patch["isRead"] = *changes.Read
		}
		if changes.Starred != nil {
			status := "notFlagged"
			if *changes.Starred {
				status = "flagged"
			}
			patch["flag"] = map[string]string{"flagStatus": status}
		}
		if len(patch) > 0 {
			if err := o.request(ctx, http.MethodPatch, o.endpoint("me", "messages", message.ID), nil, patch, nil); err != nil {
				return ThreadChangeResult{}, err
			}
		}
		if changes.Archived != nil {
			destination := "inbox"
			if *changes.Archived {
				destination = "archive"
			}
			payload := map[string]string{"destinationId": destination}
			if err := o.request(ctx, http.MethodPost, o.endpoint("me", "messages", message.ID, "move"), nil, payload, nil); err != nil {
				return ThreadChangeResult{}, err
			}
		}
	}
	result := ThreadChangeResult{ThreadID: conversationID}
	if changes.Read != nil {
		if *changes.Read {
			result.RemovedLabels = append(result.RemovedLabels, "UNREAD")
		} else {
			result.AddedLabels = append(result.AddedLabels, "UNREAD")
		}
	}
	if changes.Archived != nil {
		if *changes.Archived {
			result.AddedLabels = append(result.AddedLabels, "ARCHIVED")
		} else {
			result.RemovedLabels = append(result.RemovedLabels, "ARCHIVED")
		}
	}
	if changes.Starred != nil {
		if *changes.Starred {
			result.AddedLabels = append(result.AddedLabels, "STARRED")
		} else {
			result.RemovedLabels = append(result.RemovedLabels, "STARRED")
		}
	}
	return result, nil
}

func (o *Outlook) CreateDraft(ctx context.Context, input DraftInput) (Draft, error) {
	payload, attachments, err := o.graphDraftPayload(input)
	if err != nil {
		return Draft{}, err
	}
	var response graphMessage
	if err := o.request(ctx, http.MethodPost, o.endpoint("me", "messages"), nil, payload, &response); err != nil {
		return Draft{}, err
	}
	if strings.TrimSpace(response.ID) == "" {
		return Draft{}, errors.New("malformed Microsoft Graph draft: missing id")
	}
	if err := o.addDraftAttachments(ctx, response.ID, attachments); err != nil {
		return Draft{}, err
	}
	if len(attachments) > 0 {
		response, err = o.getMessage(ctx, response.ID)
		if err != nil {
			return Draft{}, err
		}
	}
	return o.normalizeGraphDraft(response)
}

func (o *Outlook) UpdateDraft(ctx context.Context, draftID string, input DraftInput) (Draft, error) {
	if strings.TrimSpace(draftID) == "" {
		return Draft{}, ErrInvalidInput
	}
	payload, attachments, err := o.graphDraftPayload(input)
	if err != nil {
		return Draft{}, err
	}
	var response graphMessage
	if err := o.request(ctx, http.MethodPatch, o.endpoint("me", "messages", draftID), nil, payload, &response); err != nil {
		return Draft{}, err
	}
	if err := o.replaceDraftAttachments(ctx, draftID, attachments); err != nil {
		return Draft{}, err
	}
	response, err = o.getMessage(ctx, draftID)
	if err != nil {
		return Draft{}, err
	}
	return o.normalizeGraphDraft(response)
}

func (o *Outlook) SendDraft(ctx context.Context, draftID string) (Message, error) {
	if strings.TrimSpace(draftID) == "" {
		return Message{}, ErrInvalidInput
	}
	// Fetching first proves the caller supplied an existing provider draft. The
	// send request itself carries no body that could manufacture a new message.
	source, err := o.getMessage(ctx, draftID)
	if err != nil {
		return Message{}, err
	}
	if !source.IsDraft {
		return Message{}, ErrInvalidInput
	}
	if err := o.request(ctx, http.MethodPost, o.endpoint("me", "messages", draftID, "send"), nil, nil, nil); err != nil {
		return Message{}, err
	}
	if source.ConversationID == "" {
		source.ConversationID = source.ID
	}
	var bodyBytes int64
	message, err := o.normalizeGraphMessage(source, &bodyBytes)
	if err != nil {
		return Message{}, err
	}
	message.Draft = false
	message.Labels = removeLabel(message.Labels, "DRAFT")
	return message, nil
}

func (o *Outlook) getMessage(ctx context.Context, id string) (graphMessage, error) {
	query := url.Values{
		"$select": []string{graphFullMessageSelect},
		"$expand": []string{"attachments($select=id,name,contentType,size,isInline)"},
	}
	var response graphMessage
	if err := o.request(ctx, http.MethodGet, o.endpoint("me", "messages", id), query, nil, &response); err != nil {
		return graphMessage{}, err
	}
	return response, nil
}

func (o *Outlook) graphDraftPayload(input DraftInput) (graphDraftWrite, []graphFileAttachment, error) {
	if containsNewline(input.Subject) || containsNewline(input.ThreadID) {
		return graphDraftWrite{}, nil, ErrInvalidInput
	}
	totalBodyBytes := int64(len(input.Text))
	if totalBodyBytes > o.maxBodyBytes {
		return graphDraftWrite{}, nil, ErrBodyTooLarge
	}
	to, err := graphRecipientsFromAddresses(input.To)
	if err != nil {
		return graphDraftWrite{}, nil, err
	}
	cc, err := graphRecipientsFromAddresses(input.Cc)
	if err != nil {
		return graphDraftWrite{}, nil, err
	}
	bcc, err := graphRecipientsFromAddresses(input.Bcc)
	if err != nil {
		return graphDraftWrite{}, nil, err
	}
	replyTo, err := graphRecipientsFromAddresses(input.ReplyTo)
	if err != nil {
		return graphDraftWrite{}, nil, err
	}
	payload := graphDraftWrite{
		Subject: input.Subject, Body: graphItemBody{ContentType: "Text", Content: input.Text},
		ToRecipients: to, CcRecipients: cc, BccRecipients: bcc, ReplyTo: replyTo,
	}
	attachments := make([]graphFileAttachment, 0, len(input.Attachments))
	for _, attachment := range input.Attachments {
		if containsNewline(attachment.Filename) || containsNewline(attachment.ContentID) {
			return graphDraftWrite{}, nil, ErrInvalidInput
		}
		totalBodyBytes += int64(len(attachment.Data))
		if totalBodyBytes > o.maxBodyBytes {
			return graphDraftWrite{}, nil, ErrBodyTooLarge
		}
		contentType := cleanHeader(attachment.ContentType)
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		attachments = append(attachments, graphFileAttachment{
			ODataType: "#microsoft.graph.fileAttachment", Name: attachment.Filename,
			ContentType: contentType, ContentBytes: attachment.Data, IsInline: attachment.Inline, ContentID: attachment.ContentID,
		})
	}
	return payload, attachments, nil
}

func (o *Outlook) addDraftAttachments(ctx context.Context, draftID string, attachments []graphFileAttachment) error {
	for _, attachment := range attachments {
		if err := o.request(ctx, http.MethodPost, o.endpoint("me", "messages", draftID, "attachments"), nil, attachment, nil); err != nil {
			return err
		}
	}
	return nil
}

func (o *Outlook) replaceDraftAttachments(ctx context.Context, draftID string, desired []graphFileAttachment) error {
	query := url.Values{"$select": []string{"id"}, "$top": []string{"100"}}
	for pageNumber := 0; pageNumber < maxGraphPages; pageNumber++ {
		var page graphAttachmentPage
		if err := o.request(ctx, http.MethodGet, o.endpoint("me", "messages", draftID, "attachments"), query, nil, &page); err != nil {
			return err
		}
		for _, attachment := range page.Value {
			if strings.TrimSpace(attachment.ID) == "" {
				return errors.New("malformed Microsoft Graph attachment: missing id")
			}
			if err := o.request(ctx, http.MethodDelete, o.endpoint("me", "messages", draftID, "attachments", attachment.ID), nil, nil, nil); err != nil {
				return err
			}
		}
		token, err := o.nextToken(page.NextLink)
		if err != nil {
			return err
		}
		if token == "" {
			return o.addDraftAttachments(ctx, draftID, desired)
		}
		query.Set("$skiptoken", token)
	}
	return ErrResponseTooLarge
}

func graphRecipientsFromAddresses(addresses []Address) ([]graphRecipient, error) {
	result := make([]graphRecipient, 0, len(addresses))
	for _, value := range addresses {
		if containsNewline(value.Name) || containsNewline(value.Email) {
			return nil, ErrInvalidInput
		}
		parsed, err := stdmail.ParseAddress(value.Email)
		if err != nil || !strings.EqualFold(parsed.Address, strings.TrimSpace(value.Email)) {
			return nil, ErrInvalidInput
		}
		result = append(result, graphRecipient{EmailAddress: graphEmailAddress{
			Name: value.Name, Address: parsed.Address,
		}})
	}
	return result, nil
}

func (o *Outlook) normalizeGraphDraft(source graphMessage) (Draft, error) {
	if strings.TrimSpace(source.ID) == "" {
		return Draft{}, errors.New("malformed Microsoft Graph draft: missing id")
	}
	if source.ConversationID == "" {
		source.ConversationID = source.ID
	}
	var bodyBytes int64
	message, err := o.normalizeGraphMessage(source, &bodyBytes)
	if err != nil {
		return Draft{}, err
	}
	return Draft{
		Provenance: Provenance{Provider: ProviderOutlook, ProviderID: source.ID, AccountID: o.accountID},
		ThreadID:   source.ConversationID, Message: message,
	}, nil
}

func removeLabel(labels []string, unwanted string) []string {
	result := make([]string, 0, len(labels))
	for _, label := range labels {
		if label != unwanted {
			result = append(result, label)
		}
	}
	return result
}

var _ Provider = (*Outlook)(nil)

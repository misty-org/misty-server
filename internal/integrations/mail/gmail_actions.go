package mail

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"mime"
	"mime/multipart"
	"net/http"
	stdmail "net/mail"
	"net/textproto"
	"strings"
)

func (g *Gmail) ModifyThread(ctx context.Context, id string, changes ThreadChanges) (ThreadChangeResult, error) {
	if strings.TrimSpace(id) == "" || (changes.Read == nil && changes.Archived == nil && changes.Starred == nil) {
		return ThreadChangeResult{}, ErrInvalidInput
	}
	add, remove := make([]string, 0, 3), make([]string, 0, 3)
	if changes.Read != nil {
		if *changes.Read {
			remove = append(remove, "UNREAD")
		} else {
			add = append(add, "UNREAD")
		}
	}
	if changes.Archived != nil {
		if *changes.Archived {
			remove = append(remove, "INBOX")
		} else {
			add = append(add, "INBOX")
		}
	}
	if changes.Starred != nil {
		if *changes.Starred {
			add = append(add, "STARRED")
		} else {
			remove = append(remove, "STARRED")
		}
	}
	payload := struct {
		Add    []string `json:"addLabelIds,omitempty"`
		Remove []string `json:"removeLabelIds,omitempty"`
	}{Add: add, Remove: remove}
	if err := g.request(ctx, http.MethodPost, g.endpoint("users", "me", "threads", id, "modify"), nil, payload, nil); err != nil {
		return ThreadChangeResult{}, err
	}
	return ThreadChangeResult{ThreadID: id, AddedLabels: add, RemovedLabels: remove}, nil
}

func (g *Gmail) CreateDraft(ctx context.Context, input DraftInput) (Draft, error) {
	raw, err := g.renderDraft(input)
	if err != nil {
		return Draft{}, err
	}
	payload := struct {
		Message struct {
			Raw      string `json:"raw"`
			ThreadID string `json:"threadId,omitempty"`
		} `json:"message"`
	}{}
	payload.Message.Raw = base64.RawURLEncoding.EncodeToString(raw)
	payload.Message.ThreadID = input.ThreadID
	var response gmailDraft
	if err := g.request(ctx, http.MethodPost, g.endpoint("users", "me", "drafts"), nil, payload, &response); err != nil {
		return Draft{}, err
	}
	return g.normalizeDraft(response)
}

func (g *Gmail) UpdateDraft(ctx context.Context, draftID string, input DraftInput) (Draft, error) {
	if strings.TrimSpace(draftID) == "" {
		return Draft{}, ErrInvalidInput
	}
	raw, err := g.renderDraft(input)
	if err != nil {
		return Draft{}, err
	}
	payload := struct {
		ID      string `json:"id"`
		Message struct {
			Raw      string `json:"raw"`
			ThreadID string `json:"threadId,omitempty"`
		} `json:"message"`
	}{ID: draftID}
	payload.Message.Raw = base64.RawURLEncoding.EncodeToString(raw)
	payload.Message.ThreadID = input.ThreadID
	var response gmailDraft
	if err := g.request(ctx, http.MethodPut, g.endpoint("users", "me", "drafts", draftID), nil, payload, &response); err != nil {
		return Draft{}, err
	}
	return g.normalizeDraft(response)
}

func (g *Gmail) SendDraft(ctx context.Context, draftID string) (Message, error) {
	if strings.TrimSpace(draftID) == "" {
		return Message{}, ErrInvalidInput
	}
	// Gmail's drafts.send endpoint only receives an existing draft ID. This
	// prevents a read or synchronization path from accidentally sending mail.
	payload := struct {
		ID string `json:"id"`
	}{ID: draftID}
	var response gmailMessage
	if err := g.request(ctx, http.MethodPost, g.endpoint("users", "me", "drafts", "send"), nil, payload, &response); err != nil {
		return Message{}, err
	}
	return g.normalizeMessage(response, response.ThreadID)
}

func (g *Gmail) normalizeDraft(source gmailDraft) (Draft, error) {
	if strings.TrimSpace(source.ID) == "" {
		return Draft{}, fmt.Errorf("malformed gmail draft: missing id")
	}
	message, err := g.normalizeMessage(source.Message, source.Message.ThreadID)
	if err != nil {
		return Draft{}, err
	}
	return Draft{
		Provenance: Provenance{Provider: ProviderGmail, ProviderID: source.ID, AccountID: g.accountID},
		ThreadID:   source.Message.ThreadID, Message: message,
	}, nil
}

func (g *Gmail) renderDraft(input DraftInput) ([]byte, error) {
	if containsNewline(input.Subject) || containsNewline(input.ThreadID) {
		return nil, ErrInvalidInput
	}
	totalBodyBytes := int64(len(input.Text))
	if totalBodyBytes > g.maxBodyBytes {
		return nil, ErrBodyTooLarge
	}
	for _, attachment := range input.Attachments {
		if containsNewline(attachment.Filename) || containsNewline(attachment.ContentID) {
			return nil, ErrInvalidInput
		}
		totalBodyBytes += int64(len(attachment.Data))
		if totalBodyBytes > g.maxBodyBytes {
			return nil, ErrBodyTooLarge
		}
	}
	to, err := renderAddresses(input.To)
	if err != nil {
		return nil, err
	}
	cc, err := renderAddresses(input.Cc)
	if err != nil {
		return nil, err
	}
	bcc, err := renderAddresses(input.Bcc)
	if err != nil {
		return nil, err
	}
	replyTo, err := renderAddresses(input.ReplyTo)
	if err != nil {
		return nil, err
	}
	var raw bytes.Buffer
	writeHeader := func(name, value string) {
		if value != "" {
			fmt.Fprintf(&raw, "%s: %s\r\n", name, value)
		}
	}
	writeHeader("To", to)
	writeHeader("Cc", cc)
	writeHeader("Bcc", bcc)
	writeHeader("Reply-To", replyTo)
	writeHeader("Subject", mime.QEncoding.Encode("utf-8", input.Subject))
	writeHeader("MIME-Version", "1.0")
	if len(input.Attachments) == 0 {
		writeHeader("Content-Type", `text/plain; charset="UTF-8"`)
		writeHeader("Content-Transfer-Encoding", "base64")
		raw.WriteString("\r\n")
		writeBase64Lines(&raw, []byte(input.Text))
	} else if err := writeMultipartDraft(&raw, input); err != nil {
		return nil, err
	}
	if int64(raw.Len()) > g.maxRequestBytes {
		return nil, ErrBodyTooLarge
	}
	return raw.Bytes(), nil
}

func writeMultipartDraft(raw *bytes.Buffer, input DraftInput) error {
	writer := multipart.NewWriter(raw)
	fmt.Fprintf(raw, "Content-Type: multipart/mixed; boundary=%q\r\n\r\n", writer.Boundary())
	textHeaders := make(textproto.MIMEHeader)
	textHeaders.Set("Content-Type", `text/plain; charset="UTF-8"`)
	textHeaders.Set("Content-Transfer-Encoding", "base64")
	textPart, err := writer.CreatePart(textHeaders)
	if err != nil {
		return err
	}
	writeBase64Lines(textPart, []byte(input.Text))
	for _, attachment := range input.Attachments {
		contentType := strings.TrimSpace(attachment.ContentType)
		mediaType, parameters, err := mime.ParseMediaType(contentType)
		if err != nil {
			contentType = "application/octet-stream"
			mediaType = contentType
			parameters = make(map[string]string)
		}
		parameters["name"] = attachment.Filename
		disposition := "attachment"
		if attachment.Inline {
			disposition = "inline"
		}
		headers := make(textproto.MIMEHeader)
		headers.Set("Content-Type", mime.FormatMediaType(mediaType, parameters))
		headers.Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": attachment.Filename}))
		headers.Set("Content-Transfer-Encoding", "base64")
		if attachment.ContentID != "" {
			headers.Set("Content-ID", "<"+attachment.ContentID+">")
		}
		part, err := writer.CreatePart(headers)
		if err != nil {
			return err
		}
		writeBase64Lines(part, attachment.Data)
	}
	return writer.Close()
}

func writeBase64Lines(writer interface{ Write([]byte) (int, error) }, data []byte) {
	encoded := base64.StdEncoding.EncodeToString(data)
	for len(encoded) > 76 {
		_, _ = writer.Write([]byte(encoded[:76] + "\r\n"))
		encoded = encoded[76:]
	}
	_, _ = writer.Write([]byte(encoded + "\r\n"))
}

func renderAddresses(addresses []Address) (string, error) {
	result := make([]string, 0, len(addresses))
	for _, value := range addresses {
		if containsNewline(value.Name) || containsNewline(value.Email) {
			return "", ErrInvalidInput
		}
		parsed, err := stdmail.ParseAddress(value.Email)
		if err != nil || !strings.EqualFold(parsed.Address, strings.TrimSpace(value.Email)) {
			return "", ErrInvalidInput
		}
		result = append(result, (&stdmail.Address{Name: value.Name, Address: parsed.Address}).String())
	}
	return strings.Join(result, ", "), nil
}

func containsNewline(value string) bool {
	return strings.ContainsAny(value, "\r\n\x00")
}

var _ Provider = (*Gmail)(nil)

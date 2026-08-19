package api

import (
	"time"

	mailintegration "github.com/kannachi323/misty/server/internal/integrations/mail"
)

type mailAddressDTO struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type mailBodyDTO struct {
	Text      string `json:"text"`
	HTML      string `json:"html,omitempty"`
	HadHTML   bool   `json:"had_html"`
	Truncated bool   `json:"truncated"`
}

type mailAttachmentDTO struct {
	Provider    string `json:"provider"`
	ProviderID  string `json:"provider_id"`
	AccountID   string `json:"account_id"`
	MessageID   string `json:"message_id"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	Inline      bool   `json:"inline"`
	ContentID   string `json:"content_id"`
}

type mailMessageDTO struct {
	Provider    string              `json:"provider"`
	ProviderID  string              `json:"provider_id"`
	AccountID   string              `json:"account_id"`
	ThreadID    string              `json:"thread_id"`
	RFC822ID    string              `json:"rfc822_id"`
	Subject     string              `json:"subject"`
	From        mailAddressDTO      `json:"from"`
	To          []mailAddressDTO    `json:"to"`
	Cc          []mailAddressDTO    `json:"cc"`
	Bcc         []mailAddressDTO    `json:"bcc"`
	ReplyTo     []mailAddressDTO    `json:"reply_to"`
	SentAt      time.Time           `json:"sent_at"`
	Snippet     string              `json:"snippet"`
	Body        mailBodyDTO         `json:"body"`
	Labels      []string            `json:"labels"`
	Unread      bool                `json:"unread"`
	Starred     bool                `json:"starred"`
	Draft       bool                `json:"draft"`
	Attachments []mailAttachmentDTO `json:"attachments"`
}

type mailThreadDTO struct {
	Provider      string           `json:"provider"`
	ProviderID    string           `json:"provider_id"`
	AccountID     string           `json:"account_id"`
	Subject       string           `json:"subject"`
	Snippet       string           `json:"snippet"`
	Participants  []mailAddressDTO `json:"participants"`
	Messages      []mailMessageDTO `json:"messages"`
	Labels        []string         `json:"labels"`
	LastMessageAt time.Time        `json:"last_message_at"`
	Unread        bool             `json:"unread"`
	Starred       bool             `json:"starred"`
}

type mailFolderDTO struct {
	Provider   string                     `json:"provider"`
	ProviderID string                     `json:"provider_id"`
	AccountID  string                     `json:"account_id"`
	Name       string                     `json:"name"`
	Kind       mailintegration.FolderKind `json:"kind"`
	System     bool                       `json:"system"`
	Total      int64                      `json:"total"`
	Unread     int64                      `json:"unread"`
	TextColor  string                     `json:"text_color"`
	Background string                     `json:"background"`
}

type mailDraftDTO struct {
	Provider   string         `json:"provider"`
	ProviderID string         `json:"provider_id"`
	AccountID  string         `json:"account_id"`
	ThreadID   string         `json:"thread_id"`
	Message    mailMessageDTO `json:"message"`
}

func mailAddressesDTO(values []mailintegration.Address) []mailAddressDTO {
	result := make([]mailAddressDTO, len(values))
	for index, value := range values {
		result[index] = mailAddressDTO{Name: value.Name, Email: value.Email}
	}
	return result
}

func mailAttachmentsDTO(values []mailintegration.Attachment) []mailAttachmentDTO {
	result := make([]mailAttachmentDTO, len(values))
	for index, value := range values {
		result[index] = mailAttachmentDTO{Provider: value.Provider, ProviderID: value.ProviderID,
			AccountID: value.AccountID, MessageID: value.MessageID, Filename: value.Filename,
			ContentType: value.ContentType, Size: value.Size, Inline: value.Inline, ContentID: value.ContentID}
	}
	return result
}

func mailMessageToDTO(value mailintegration.Message) mailMessageDTO {
	return mailMessageDTO{Provider: value.Provider, ProviderID: value.ProviderID, AccountID: value.AccountID,
		ThreadID: value.ThreadID, RFC822ID: value.RFC822ID, Subject: value.Subject,
		From: mailAddressDTO{Name: value.From.Name, Email: value.From.Email}, To: mailAddressesDTO(value.To),
		Cc: mailAddressesDTO(value.Cc), Bcc: mailAddressesDTO(value.Bcc), ReplyTo: mailAddressesDTO(value.ReplyTo),
		SentAt: value.SentAt, Snippet: value.Snippet,
		Body:   mailBodyDTO{Text: value.Body.Text, HTML: value.Body.HTML, HadHTML: value.Body.HadHTML, Truncated: value.Body.Truncated},
		Labels: append([]string{}, value.Labels...), Unread: value.Unread, Starred: value.Starred,
		Draft: value.Draft, Attachments: mailAttachmentsDTO(value.Attachments)}
}

func mailThreadToDTO(value mailintegration.Thread) mailThreadDTO {
	messages := make([]mailMessageDTO, len(value.Messages))
	for index, message := range value.Messages {
		messages[index] = mailMessageToDTO(message)
	}
	return mailThreadDTO{Provider: value.Provider, ProviderID: value.ProviderID, AccountID: value.AccountID,
		Subject: value.Subject, Snippet: value.Snippet, Participants: mailAddressesDTO(value.Participants),
		Messages: messages, Labels: append([]string{}, value.Labels...), LastMessageAt: value.LastMessageAt,
		Unread: value.Unread, Starred: value.Starred}
}

func mailFolderToDTO(value mailintegration.Folder) mailFolderDTO {
	return mailFolderDTO{Provider: value.Provider, ProviderID: value.ProviderID, AccountID: value.AccountID,
		Name: value.Name, Kind: value.Kind, System: value.System, Total: value.Total, Unread: value.Unread,
		TextColor: value.TextColor, Background: value.Background}
}

func mailDraftToDTO(value mailintegration.Draft) mailDraftDTO {
	return mailDraftDTO{Provider: value.Provider, ProviderID: value.ProviderID, AccountID: value.AccountID,
		ThreadID: value.ThreadID, Message: mailMessageToDTO(value.Message)}
}

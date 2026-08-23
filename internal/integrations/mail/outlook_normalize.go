package mail

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

func normalizeGraphFolder(accountID string, source graphFolder) Folder {
	kind := graphFolderKind(source)
	return Folder{
		Provenance: Provenance{Provider: ProviderOutlook, ProviderID: source.ID, AccountID: accountID},
		Name:       cleanHeader(source.DisplayName), Kind: kind, System: source.WellKnownName != "" || kind != FolderCustom,
		Total: source.TotalItemCount, Unread: source.UnreadItemCount,
	}
}

func graphFolderKind(source graphFolder) FolderKind {
	name := strings.ToLower(strings.TrimSpace(source.WellKnownName))
	if name == "" {
		name = strings.ToLower(strings.TrimSpace(source.DisplayName))
	}
	switch name {
	case "inbox":
		return FolderInbox
	case "sentitems", "sent items", "sent":
		return FolderSent
	case "drafts":
		return FolderDrafts
	case "deleteditems", "deleted items", "trash":
		return FolderTrash
	case "junkemail", "junk email", "spam":
		return FolderSpam
	case "archive":
		return FolderCustom
	default:
		return FolderCustom
	}
}

func (o *Outlook) normalizeGraphThreads(messages []graphMessage) ([]Thread, error) {
	groups := make(map[string][]graphMessage)
	order := make([]string, 0)
	for _, message := range messages {
		if strings.TrimSpace(message.ID) == "" || strings.TrimSpace(message.ConversationID) == "" {
			return nil, errors.New("malformed Microsoft Graph message identity")
		}
		if _, exists := groups[message.ConversationID]; !exists {
			order = append(order, message.ConversationID)
		}
		groups[message.ConversationID] = append(groups[message.ConversationID], message)
	}
	threads := make([]Thread, 0, len(groups))
	for _, conversationID := range order {
		thread, err := o.normalizeGraphThread(conversationID, groups[conversationID])
		if err != nil {
			return nil, err
		}
		threads = append(threads, thread)
	}
	sort.SliceStable(threads, func(i, j int) bool {
		return threads[i].LastMessageAt.After(threads[j].LastMessageAt)
	})
	return threads, nil
}

func (o *Outlook) normalizeGraphThread(conversationID string, sources []graphMessage) (Thread, error) {
	thread := Thread{
		Provenance: Provenance{Provider: ProviderOutlook, ProviderID: conversationID, AccountID: o.accountID},
	}
	labelSet := make(map[string]bool)
	participantSet := make(map[string]bool)
	var bodyBytes int64
	for _, source := range sources {
		message, err := o.normalizeGraphMessage(source, &bodyBytes)
		if err != nil {
			return Thread{}, err
		}
		thread.Messages = append(thread.Messages, message)
		if thread.Subject == "" && message.Subject != "" {
			thread.Subject = message.Subject
		}
		if message.SentAt.After(thread.LastMessageAt) {
			thread.LastMessageAt = message.SentAt
			thread.Snippet = message.Snippet
		}
		thread.Unread = thread.Unread || message.Unread
		thread.Starred = thread.Starred || message.Starred
		for _, label := range message.Labels {
			labelSet[label] = true
		}
		addresses := append([]Address{message.From}, message.Cc...)
		for _, address := range addresses {
			key := strings.ToLower(address.Email)
			if key != "" && !participantSet[key] {
				participantSet[key] = true
				thread.Participants = append(thread.Participants, address)
			}
		}
	}
	for label := range labelSet {
		thread.Labels = append(thread.Labels, label)
	}
	sort.Strings(thread.Labels)
	sort.SliceStable(thread.Messages, func(i, j int) bool {
		return thread.Messages[i].SentAt.Before(thread.Messages[j].SentAt)
	})
	return thread, nil
}

func (o *Outlook) normalizeGraphMessage(source graphMessage, totalBodyBytes *int64) (Message, error) {
	if strings.TrimSpace(source.ID) == "" || strings.TrimSpace(source.ConversationID) == "" {
		return Message{}, errors.New("malformed Microsoft Graph message identity")
	}
	bodyContent := source.Body.Content
	*totalBodyBytes += int64(len(bodyContent))
	if *totalBodyBytes > o.maxBodyBytes {
		return Message{}, ErrBodyTooLarge
	}
	body := Body{}
	switch strings.ToLower(strings.TrimSpace(source.Body.ContentType)) {
	case "html":
		body.Text = cleanText(htmlToText(bodyContent))
		body.HTML = cleanText(bodyContent)
		body.HadHTML = true
	case "text", "":
		body.Text = cleanText(bodyContent)
	default:
		return Message{}, fmt.Errorf("unsupported Microsoft Graph body type %q", source.Body.ContentType)
	}
	labels := make([]string, 0, 4)
	if source.ParentFolderID != "" {
		labels = append(labels, source.ParentFolderID)
	}
	if !source.IsRead {
		labels = append(labels, "UNREAD")
	}
	if graphFlagged(source.Flag) {
		labels = append(labels, "STARRED")
	}
	if source.IsDraft {
		labels = append(labels, "DRAFT")
	}
	sort.Strings(labels)
	attachments := make([]Attachment, 0, len(source.Attachments))
	for index, attachment := range source.Attachments {
		providerID := attachment.ID
		if providerID == "" {
			providerID = fmt.Sprintf("%s:attachment:%d", source.ID, index)
		}
		attachments = append(attachments, Attachment{
			Provenance: Provenance{Provider: ProviderOutlook, ProviderID: providerID, AccountID: o.accountID},
			MessageID:  source.ID, Filename: cleanHeader(attachment.Name), ContentType: cleanHeader(attachment.ContentType),
			Size: attachment.Size, Inline: attachment.IsInline, ContentID: cleanHeader(attachment.ContentID),
		})
	}
	return Message{
		Provenance: Provenance{Provider: ProviderOutlook, ProviderID: source.ID, AccountID: o.accountID},
		ThreadID:   source.ConversationID, RFC822ID: cleanHeader(source.InternetMessageID), Subject: cleanHeader(source.Subject),
		From: graphAddress(source.From), To: graphAddresses(source.ToRecipients), Cc: graphAddresses(source.CcRecipients),
		Bcc: graphAddresses(source.BccRecipients), ReplyTo: graphAddresses(source.ReplyTo), SentAt: graphMessageTime(source),
		Snippet: cleanText(source.BodyPreview), Body: body, Labels: labels, Unread: !source.IsRead,
		Starred: graphFlagged(source.Flag), Draft: source.IsDraft, Attachments: attachments,
	}, nil
}

func graphAddress(recipient graphRecipient) Address {
	return Address{Name: cleanHeader(recipient.EmailAddress.Name), Email: cleanHeader(recipient.EmailAddress.Address)}
}

func graphAddresses(recipients []graphRecipient) []Address {
	result := make([]Address, 0, len(recipients))
	for _, recipient := range recipients {
		address := graphAddress(recipient)
		if address.Email != "" {
			result = append(result, address)
		}
	}
	return result
}

func graphMessageTime(source graphMessage) time.Time {
	for _, value := range []string{source.SentDateTime, source.ReceivedDateTime} {
		if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}

func graphFlagged(flag graphFlag) bool {
	return strings.EqualFold(flag.FlagStatus, "flagged")
}

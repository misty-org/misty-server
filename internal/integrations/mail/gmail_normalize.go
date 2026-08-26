package mail

import (
	"encoding/base64"
	"fmt"
	"html"
	"mime"
	stdmail "net/mail"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	xhtml "golang.org/x/net/html"
)

type normalizedParts struct {
	plain       []string
	html        []string
	attachments []Attachment
	totalBytes  int64
}

func (g *Gmail) normalizeThread(source gmailThread) (Thread, error) {
	if strings.TrimSpace(source.ID) == "" {
		return Thread{}, fmt.Errorf("malformed gmail thread: missing id")
	}
	thread := Thread{
		Provenance: Provenance{Provider: ProviderGmail, ProviderID: source.ID, AccountID: g.accountID},
		Snippet:    cleanText(source.Snippet),
	}
	labelSet := make(map[string]bool)
	participantSet := make(map[string]bool)
	for _, raw := range source.Messages {
		message, err := g.normalizeMessage(raw, source.ID)
		if err != nil {
			return Thread{}, err
		}
		thread.Messages = append(thread.Messages, message)
		if thread.Subject == "" && message.Subject != "" {
			thread.Subject = message.Subject
		}
		if message.SentAt.After(thread.LastMessageAt) {
			thread.LastMessageAt = message.SentAt
		}
		thread.Unread = thread.Unread || message.Unread
		thread.Starred = thread.Starred || message.Starred
		for _, label := range message.Labels {
			labelSet[label] = true
		}
		for _, address := range append([]Address{message.From}, message.Cc...) {
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
	sort.SliceStable(thread.Messages, func(i, j int) bool { return thread.Messages[i].SentAt.Before(thread.Messages[j].SentAt) })
	return thread, nil
}

func (g *Gmail) normalizeMessage(source gmailMessage, fallbackThreadID string) (Message, error) {
	if strings.TrimSpace(source.ID) == "" {
		return Message{}, fmt.Errorf("malformed gmail message: missing id")
	}
	threadID := source.ThreadID
	if threadID == "" {
		threadID = fallbackThreadID
	}
	headers := selectedHeaders(source.Payload.Headers)
	parts := normalizedParts{}
	if err := g.walkPart(source.ID, source.Payload, &parts); err != nil {
		return Message{}, err
	}
	body := Body{}
	if len(parts.plain) > 0 {
		body.Text = strings.TrimSpace(strings.Join(parts.plain, "\n\n"))
	} else if len(parts.html) > 0 {
		body.Text = strings.TrimSpace(htmlToText(strings.Join(parts.html, "\n")))
	}
	if len(parts.html) > 0 {
		body.HTML = strings.TrimSpace(strings.Join(parts.html, "\n"))
	}
	body.HadHTML = len(parts.html) > 0
	labels := append([]string(nil), source.LabelIDs...)
	sort.Strings(labels)
	message := Message{
		Provenance: Provenance{Provider: ProviderGmail, ProviderID: source.ID, AccountID: g.accountID},
		ThreadID:   threadID, RFC822ID: headers["message-id"], Subject: headers["subject"],
		From: firstAddress(headers["from"]), To: parseAddresses(headers["to"]),
		Cc: parseAddresses(headers["cc"]), Bcc: parseAddresses(headers["bcc"]),
		ReplyTo: parseAddresses(headers["reply-to"]), SentAt: parseMessageTime(headers["date"], source.InternalDate),
		Snippet: cleanText(source.Snippet), Body: body, Labels: labels,
		Unread: hasLabel(labels, "UNREAD"), Starred: hasLabel(labels, "STARRED"),
		Draft: hasLabel(labels, "DRAFT"), Attachments: parts.attachments,
	}
	return message, nil
}

func (g *Gmail) walkPart(messageID string, part gmailPart, result *normalizedParts) error {
	mediaType := strings.ToLower(strings.TrimSpace(part.MimeType))
	if parsed, _, err := mime.ParseMediaType(mediaType); err == nil {
		mediaType = parsed
	}
	disposition := strings.ToLower(headerValue(part.Headers, "Content-Disposition"))
	contentID := strings.Trim(headerValue(part.Headers, "Content-ID"), "<> \t")
	isAttachment := part.Filename != "" || part.Body.AttachmentID != "" || strings.HasPrefix(disposition, "attachment")
	if isAttachment {
		providerID := part.Body.AttachmentID
		if providerID == "" {
			providerID = messageID + ":part:" + part.PartID
		}
		result.attachments = append(result.attachments, Attachment{
			Provenance: Provenance{Provider: ProviderGmail, ProviderID: providerID, AccountID: g.accountID},
			MessageID:  messageID, Filename: cleanHeader(part.Filename), ContentType: mediaType,
			Size: part.Body.Size, Inline: strings.HasPrefix(disposition, "inline") || contentID != "", ContentID: contentID,
		})
	} else if part.Body.Data != "" && (mediaType == "text/plain" || mediaType == "text/html") {
		decoded, err := decodeGmailData(part.Body.Data, g.maxBodyBytes-result.totalBytes)
		if err != nil {
			return err
		}
		result.totalBytes += int64(len(decoded))
		if result.totalBytes > g.maxBodyBytes {
			return ErrBodyTooLarge
		}
		text := cleanText(string(decoded))
		if mediaType == "text/plain" {
			result.plain = append(result.plain, text)
		} else {
			result.html = append(result.html, text)
		}
	}
	for _, child := range part.Parts {
		if err := g.walkPart(messageID, child, result); err != nil {
			return err
		}
	}
	return nil
}

func decodeGmailData(value string, remaining int64) ([]byte, error) {
	if remaining < 0 || int64(len(value)) > ((remaining+2)/3)*4+4 {
		return nil, ErrBodyTooLarge
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		data, err = base64.URLEncoding.DecodeString(value)
	}
	if err != nil {
		return nil, fmt.Errorf("malformed gmail MIME body: %w", err)
	}
	if int64(len(data)) > remaining {
		return nil, ErrBodyTooLarge
	}
	return data, nil
}

func selectedHeaders(headers []gmailHeader) map[string]string {
	allowed := map[string]bool{
		"subject": true, "from": true, "to": true, "cc": true, "bcc": true,
		"reply-to": true, "date": true, "message-id": true,
	}
	selected := make(map[string]string)
	for _, header := range headers {
		name := strings.ToLower(strings.TrimSpace(header.Name))
		if allowed[name] && selected[name] == "" {
			selected[name] = cleanHeader(decodeHeader(header.Value))
		}
	}
	return selected
}

func headerValue(headers []gmailHeader, wanted string) string {
	for _, header := range headers {
		if strings.EqualFold(header.Name, wanted) {
			return cleanHeader(header.Value)
		}
	}
	return ""
}

func decodeHeader(value string) string {
	decoded, err := (&mime.WordDecoder{}).DecodeHeader(value)
	if err != nil {
		return value
	}
	return decoded
}

func parseAddresses(value string) []Address {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parsed, err := stdmail.ParseAddressList(value)
	if err != nil {
		return nil
	}
	result := make([]Address, 0, len(parsed))
	for _, address := range parsed {
		if strings.TrimSpace(address.Address) != "" {
			result = append(result, Address{Name: cleanHeader(decodeHeader(address.Name)), Email: strings.TrimSpace(address.Address)})
		}
	}
	return result
}

func firstAddress(value string) Address {
	addresses := parseAddresses(value)
	if len(addresses) == 0 {
		return Address{}
	}
	return addresses[0]
}

func parseMessageTime(header, internal string) time.Time {
	if parsed, err := stdmail.ParseDate(header); err == nil {
		return parsed.UTC()
	}
	milliseconds, err := strconv.ParseInt(internal, 10, 64)
	if err != nil || milliseconds < 0 {
		return time.Time{}
	}
	return time.UnixMilli(milliseconds).UTC()
}

func hasLabel(labels []string, wanted string) bool {
	for _, label := range labels {
		if label == wanted {
			return true
		}
	}
	return false
}

func cleanHeader(value string) string {
	value = html.UnescapeString(value)
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == 0 || (unicode.IsControl(r) && r != '\t') {
			return ' '
		}
		return r
	}, value))
}

func cleanText(value string) string {
	value = html.UnescapeString(value)
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == 0 || (unicode.IsControl(r) && r != '\n' && r != '\t') {
			return -1
		}
		return r
	}, value))
}

func htmlToText(value string) string {
	document, err := xhtml.Parse(strings.NewReader(value))
	if err != nil {
		return ""
	}
	var result strings.Builder
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node.Type == xhtml.ElementNode && isIgnoredHTMLTag(node.Data) {
			return
		}
		if node.Type == xhtml.ElementNode && isBlockHTMLTag(node.Data) {
			result.WriteByte('\n')
		}
		if node.Type == xhtml.TextNode {
			result.WriteString(node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
		if node.Type == xhtml.ElementNode && isBlockHTMLTag(node.Data) {
			result.WriteByte('\n')
		}
	}
	walk(document)
	lines := strings.Fields(result.String())
	return strings.Join(lines, " ")
}

func isIgnoredHTMLTag(name string) bool {
	switch strings.ToLower(name) {
	case "script", "style", "iframe", "object", "embed", "form", "svg", "math":
		return true
	default:
		return false
	}
}

func isBlockHTMLTag(name string) bool {
	switch strings.ToLower(name) {
	case "p", "div", "br", "li", "tr", "blockquote", "h1", "h2", "h3", "h4", "h5", "h6":
		return true
	default:
		return false
	}
}

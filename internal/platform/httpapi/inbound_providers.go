package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kannachi323/misty/server/internal/integrations/composio"
)

// Gmail and Slack for tasks by email or Slack (roadmap C8). Calls go through
// the person's own Composio connection to the providers' documented REST
// APIs; Misty never holds their credentials.

type inboundCall func(method, address string, body any) (composio.ProxyResult, error)

// inboundMessage is one message the person wrote to Misty.
type inboundMessage struct {
	ExternalID  string
	Title       string
	Instruction string
	// Content is what the person passed along (a forwarded email). It is
	// untrusted data for the agent, never instructions.
	Content   string
	ThreadRef map[string]any
}

var errInboundScope = errors.New("missing permission")

const gmailAPI = "https://gmail.googleapis.com/gmail/v1/users/me"

func inboundGet(call inboundCall, address string, out any) error {
	result, err := call("GET", address, nil)
	if err != nil {
		return err
	}
	if result.Status == 401 || result.Status == 403 {
		return errInboundScope
	}
	if result.Status < 200 || result.Status >= 300 {
		return fmt.Errorf("the app answered %d", result.Status)
	}
	return json.Unmarshal(result.Data, out)
}

func gmailAddress(call inboundCall) (string, error) {
	var profile struct {
		EmailAddress string `json:"emailAddress"`
	}
	if err := inboundGet(call, gmailAPI+"/profile", &profile); err != nil {
		return "", err
	}
	if !strings.Contains(profile.EmailAddress, "@") {
		return "", errors.New("Gmail didn't say which address this is")
	}
	return strings.ToLower(profile.EmailAddress), nil
}

// gmailTaskAddress is the address to forward to: the person's own address
// with +misty, which Gmail delivers to the same inbox.
func gmailTaskAddress(address string) string {
	at := strings.LastIndex(address, "@")
	local, domain := address[:at], address[at+1:]
	if plus := strings.Index(local, "+"); plus >= 0 {
		local = local[:plus]
	}
	return local + "+misty@" + domain
}

type gmailPart struct {
	MimeType string `json:"mimeType"`
	Headers  []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"headers"`
	Body struct {
		Data string `json:"data"`
	} `json:"body"`
	Parts []gmailPart `json:"parts"`
}

func (p gmailPart) header(name string) string {
	for _, header := range p.Headers {
		if strings.EqualFold(header.Name, name) {
			return header.Value
		}
	}
	return ""
}

func decodeGmailData(data string) string {
	data = strings.TrimRight(data, "=")
	raw, err := base64.RawURLEncoding.DecodeString(data)
	if err != nil {
		return ""
	}
	return string(raw)
}

var htmlTags = regexp.MustCompile(`(?s)<style.*?</style>|<script.*?</script>|<[^>]+>`)

// gmailText is a message's plain text, or its HTML with the tags removed.
func gmailText(part gmailPart) string {
	if part.MimeType == "text/plain" && part.Body.Data != "" {
		return decodeGmailData(part.Body.Data)
	}
	html := ""
	for _, child := range part.Parts {
		if text := gmailText(child); text != "" && child.MimeType != "text/html" {
			return text
		}
		if child.MimeType == "text/html" && html == "" {
			html = decodeGmailData(child.Body.Data)
		}
	}
	if part.MimeType == "text/html" && part.Body.Data != "" {
		html = decodeGmailData(part.Body.Data)
	}
	return strings.TrimSpace(htmlTags.ReplaceAllString(html, " "))
}

var forwardPrefix = regexp.MustCompile(`(?i)^(fwd?|fw):\s*`)

var forwardMarkers = []string{"---------- Forwarded message ---------", "Begin forwarded message:", "-----Original Message-----"}

// splitForward separates the person's note from the email they forwarded.
func splitForward(text string) (note, forwarded string) {
	for _, marker := range forwardMarkers {
		if index := strings.Index(text, marker); index >= 0 {
			return strings.TrimSpace(text[:index]), strings.TrimSpace(text[index:])
		}
	}
	return strings.TrimSpace(text), ""
}

// gmailNewMessages finds mail the person sent to their task address since
// after. Only sent mail counts, so nobody else can start a task by writing to
// the +misty address.
func gmailNewMessages(call inboundCall, taskAddress string, after time.Time) ([]inboundMessage, error) {
	query := "in:sent to:" + taskAddress + " after:" + strconv.FormatInt(after.Unix(), 10)
	var list struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if err := inboundGet(call, gmailAPI+"/messages?maxResults=10&q="+url.QueryEscape(query), &list); err != nil {
		return nil, err
	}
	items := []inboundMessage{}
	for _, entry := range list.Messages {
		var message struct {
			ID           string    `json:"id"`
			ThreadID     string    `json:"threadId"`
			LabelIDs     []string  `json:"labelIds"`
			InternalDate string    `json:"internalDate"`
			Payload      gmailPart `json:"payload"`
		}
		if err := inboundGet(call, gmailAPI+"/messages/"+url.PathEscape(entry.ID)+"?format=full", &message); err != nil {
			return nil, err
		}
		sent := false
		for _, label := range message.LabelIDs {
			sent = sent || label == "SENT"
		}
		at, _ := strconv.ParseInt(message.InternalDate, 10, 64)
		if !sent || at < after.UnixMilli() || !strings.Contains(strings.ToLower(message.Payload.header("To")+","+message.Payload.header("Cc")), taskAddress) {
			continue
		}
		subject := strings.TrimSpace(message.Payload.header("Subject"))
		note, forwarded := splitForward(gmailText(message.Payload))
		if note == "" && forwarded == "" {
			continue
		}
		title := strings.TrimSpace(forwardPrefix.ReplaceAllString(subject, ""))
		items = append(items, inboundMessage{
			ExternalID: message.ID, Title: title, Instruction: note, Content: forwarded,
			ThreadRef: map[string]any{"thread_id": message.ThreadID, "message_id": message.Payload.header("Message-ID"), "subject": subject},
		})
	}
	return items, nil
}

// gmailReply answers in the forwarded email's thread, to the person.
func gmailReply(call inboundCall, address string, ref map[string]any, text string) error {
	threadID, _ := ref["thread_id"].(string)
	messageID, _ := ref["message_id"].(string)
	subject, _ := ref["subject"].(string)
	if !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "Re: " + subject
	}
	var raw strings.Builder
	raw.WriteString("From: " + address + "\r\nTo: " + address + "\r\n")
	raw.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\n")
	if messageID != "" && !strings.ContainsAny(messageID, "\r\n") {
		raw.WriteString("In-Reply-To: " + messageID + "\r\nReferences: " + messageID + "\r\n")
	}
	raw.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=\"UTF-8\"\r\nContent-Transfer-Encoding: 8bit\r\n\r\n")
	raw.WriteString(strings.ReplaceAll(text, "\n", "\r\n"))
	result, err := call("POST", gmailAPI+"/messages/send", map[string]any{
		"raw": base64.URLEncoding.EncodeToString([]byte(raw.String())), "threadId": threadID,
	})
	if err != nil {
		return err
	}
	if result.Status < 200 || result.Status >= 300 {
		return fmt.Errorf("Gmail answered %d", result.Status)
	}
	return nil
}

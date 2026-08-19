package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultGraphBaseURL    = "https://graph.microsoft.com/v1.0"
	maxGraphPages          = 20
	graphMessageSelect     = "id,conversationId,internetMessageId,subject,bodyPreview,from,toRecipients,ccRecipients,bccRecipients,replyTo,receivedDateTime,sentDateTime,isRead,isDraft,parentFolderId,flag"
	graphFullMessageSelect = graphMessageSelect + ",body,hasAttachments"
)

type OutlookConfig struct {
	BaseURL          string
	AccessToken      string
	AccountID        string
	HTTPClient       *http.Client
	Timeout          time.Duration
	MaxResponseBytes int64
	MaxRequestBytes  int64
	MaxBodyBytes     int64
	MaxMessages      int
}

type Outlook struct {
	baseURL          *url.URL
	accessToken      string
	accountID        string
	client           *http.Client
	timeout          time.Duration
	maxResponseBytes int64
	maxRequestBytes  int64
	maxBodyBytes     int64
	maxMessages      int
}

func NewOutlook(config OutlookConfig) (*Outlook, error) {
	base := strings.TrimSpace(config.BaseURL)
	if base == "" {
		base = defaultGraphBaseURL
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || strings.TrimSpace(config.AccessToken) == "" {
		return nil, ErrInvalidConfiguration
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	clone := *client
	clone.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	timeout := config.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	maxResponse := config.MaxResponseBytes
	if maxResponse <= 0 {
		maxResponse = 16 << 20
	}
	maxRequest := config.MaxRequestBytes
	if maxRequest <= 0 {
		maxRequest = 25 << 20
	}
	maxBody := config.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = 10 << 20
	}
	maxMessages := config.MaxMessages
	if maxMessages <= 0 {
		maxMessages = 500
	}
	accountID := strings.TrimSpace(config.AccountID)
	if accountID == "" {
		accountID = "me"
	}
	return &Outlook{
		baseURL: parsed, accessToken: strings.TrimSpace(config.AccessToken), accountID: accountID,
		client: &clone, timeout: timeout, maxResponseBytes: maxResponse,
		maxRequestBytes: maxRequest, maxBodyBytes: maxBody, maxMessages: maxMessages,
	}, nil
}

func (o *Outlook) endpoint(parts ...string) string {
	result := strings.TrimRight(o.baseURL.String(), "/")
	for _, part := range parts {
		result += "/" + url.PathEscape(part)
	}
	return result
}

func (o *Outlook) request(ctx context.Context, method, endpoint string, query url.Values, input, output any) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode Microsoft Graph request: %w", err)
		}
		if int64(len(encoded)) > o.maxRequestBytes {
			return ErrBodyTooLarge
		}
		body = bytes.NewReader(encoded)
	}
	requestCtx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, method, endpoint, body)
	if err != nil {
		return fmt.Errorf("create Microsoft Graph request: %w", err)
	}
	request.URL.RawQuery = query.Encode()
	if int64(len(request.URL.String())) > o.maxRequestBytes {
		return ErrBodyTooLarge
	}
	request.Header.Set("Authorization", "Bearer "+o.accessToken)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Prefer", `IdType="ImmutableId"`)
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := o.client.Do(request)
	if err != nil {
		return fmt.Errorf("Microsoft Graph request: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, o.maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read Microsoft Graph response: %w", err)
	}
	if int64(len(data)) > o.maxResponseBytes {
		return ErrResponseTooLarge
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return decodeGraphError(response.StatusCode, data)
	}
	if output == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, output); err != nil {
		return fmt.Errorf("decode Microsoft Graph response: %w", err)
	}
	return nil
}

func decodeGraphError(status int, data []byte) error {
	var envelope graphErrorEnvelope
	_ = json.Unmarshal(data, &envelope)
	message := strings.TrimSpace(envelope.Error.Message)
	if message == "" {
		message = http.StatusText(status)
	}
	return &ProviderError{
		StatusCode: status,
		Code:       strings.TrimSpace(envelope.Error.Code),
		Message:    message,
	}
}

func (o *Outlook) Account(ctx context.Context) (Account, error) {
	query := url.Values{"$select": []string{"id,displayName,mail,userPrincipalName"}}
	var user graphUser
	if err := o.request(ctx, http.MethodGet, o.endpoint("me"), query, nil, &user); err != nil {
		return Account{}, err
	}
	email := strings.TrimSpace(user.Mail)
	if email == "" {
		email = strings.TrimSpace(user.UserPrincipalName)
	}
	if strings.TrimSpace(user.ID) == "" || email == "" {
		return Account{}, errors.New("Microsoft Graph profile is missing identity")
	}
	return Account{
		Provenance: Provenance{Provider: ProviderOutlook, ProviderID: user.ID, AccountID: o.accountID},
		Email:      email, DisplayName: cleanHeader(user.DisplayName),
	}, nil
}

func (o *Outlook) ListFolders(ctx context.Context) ([]Folder, error) {
	query := url.Values{
		"$select": []string{"id,displayName,wellKnownName,totalItemCount,unreadItemCount"},
		"$top":    []string{"100"},
	}
	folders := make([]Folder, 0)
	for pageNumber := 0; pageNumber < maxGraphPages; pageNumber++ {
		var page graphFolderPage
		if err := o.request(ctx, http.MethodGet, o.endpoint("me", "mailFolders"), query, nil, &page); err != nil {
			return nil, err
		}
		for _, source := range page.Value {
			if strings.TrimSpace(source.ID) == "" {
				continue
			}
			folders = append(folders, normalizeGraphFolder(o.accountID, source))
			if len(folders) > o.maxMessages {
				return nil, ErrResponseTooLarge
			}
		}
		token, err := o.nextToken(page.NextLink)
		if err != nil {
			return nil, err
		}
		if token == "" {
			return folders, nil
		}
		query.Set("$skiptoken", token)
	}
	return nil, ErrResponseTooLarge
}

func (o *Outlook) ListThreads(ctx context.Context, input ListThreadsRequest) (ThreadPage, error) {
	if input.PageSize < 0 || input.PageSize > 500 {
		return ThreadPage{}, ErrInvalidInput
	}
	query := o.messageQuery(false)
	if input.PageSize > 0 {
		query.Set("$top", strconv.Itoa(input.PageSize))
	}
	if input.PageToken != "" {
		query.Set("$skiptoken", input.PageToken)
	}
	if input.Query != "" {
		query.Set("$search", `"`+strings.ReplaceAll(input.Query, `"`, `\"`)+`"`)
		query.Del("$orderby")
	}
	if len(input.FolderIDs) > 0 {
		filters := make([]string, 0, len(input.FolderIDs))
		for _, id := range input.FolderIDs {
			if strings.TrimSpace(id) != "" {
				filters = append(filters, "parentFolderId eq "+odataString(id))
			}
		}
		if len(filters) > 0 {
			query.Set("$filter", "("+strings.Join(filters, " or ")+")")
			query.Del("$orderby")
		}
	}
	var response graphMessagePage
	if err := o.request(ctx, http.MethodGet, o.endpoint("me", "messages"), query, nil, &response); err != nil {
		return ThreadPage{}, err
	}
	token, err := o.nextToken(response.NextLink)
	if err != nil {
		return ThreadPage{}, err
	}
	threads, err := o.normalizeGraphThreads(response.Value)
	if err != nil {
		return ThreadPage{}, err
	}
	return ThreadPage{Threads: threads, NextPageToken: token, EstimatedTotal: int64(len(threads))}, nil
}

func (o *Outlook) GetThread(ctx context.Context, conversationID string) (Thread, error) {
	if strings.TrimSpace(conversationID) == "" {
		return Thread{}, ErrInvalidInput
	}
	query := o.messageQuery(true)
	query.Set("$filter", "conversationId eq "+odataString(conversationID))
	query.Del("$orderby")
	messages, err := o.readAllMessages(ctx, query)
	if err != nil {
		return Thread{}, err
	}
	if len(messages) == 0 {
		return Thread{}, &ProviderError{StatusCode: http.StatusNotFound, Message: "conversation not found"}
	}
	threads, err := o.normalizeGraphThreads(messages)
	if err != nil {
		return Thread{}, err
	}
	if len(threads) != 1 || threads[0].ProviderID != conversationID {
		return Thread{}, errors.New("Microsoft Graph returned mismatched conversation")
	}
	return threads[0], nil
}

func (o *Outlook) messageQuery(full bool) url.Values {
	selectFields := graphMessageSelect
	if full {
		selectFields = graphFullMessageSelect
	}
	query := url.Values{
		"$select":  []string{selectFields},
		"$orderby": []string{"receivedDateTime desc"},
		"$top":     []string{"100"},
	}
	if full {
		// contentId exists on fileAttachment, but not on Graph's base attachment
		// type used while parsing an expanded collection. Asking for it here makes
		// the entire message query fail before Graph can materialize subclasses.
		query.Set("$expand", "attachments($select=id,name,contentType,size,isInline)")
	}
	return query
}

func (o *Outlook) readAllMessages(ctx context.Context, query url.Values) ([]graphMessage, error) {
	messages := make([]graphMessage, 0)
	for pageNumber := 0; pageNumber < maxGraphPages; pageNumber++ {
		var page graphMessagePage
		if err := o.request(ctx, http.MethodGet, o.endpoint("me", "messages"), query, nil, &page); err != nil {
			return nil, err
		}
		messages = append(messages, page.Value...)
		if len(messages) > o.maxMessages {
			return nil, ErrResponseTooLarge
		}
		token, err := o.nextToken(page.NextLink)
		if err != nil {
			return nil, err
		}
		if token == "" {
			return messages, nil
		}
		query.Set("$skiptoken", token)
	}
	return nil, ErrResponseTooLarge
}

func (o *Outlook) nextToken(nextLink string) (string, error) {
	if strings.TrimSpace(nextLink) == "" {
		return "", nil
	}
	parsed, err := url.Parse(nextLink)
	if err != nil {
		return "", errors.New("malformed Microsoft Graph next link")
	}
	if parsed.IsAbs() && (!strings.EqualFold(parsed.Scheme, o.baseURL.Scheme) || !strings.EqualFold(parsed.Host, o.baseURL.Host)) {
		return "", errors.New("untrusted Microsoft Graph next link")
	}
	token := parsed.Query().Get("$skiptoken")
	if token == "" {
		return "", errors.New("Microsoft Graph next link is missing skip token")
	}
	return token, nil
}

func odataString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

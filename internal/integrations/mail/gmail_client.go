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
	"sync"
	"time"
)

const defaultGmailBaseURL = "https://gmail.googleapis.com/gmail/v1"

type GmailConfig struct {
	BaseURL          string
	AccessToken      string
	AccountID        string
	HTTPClient       *http.Client
	Timeout          time.Duration
	MaxResponseBytes int64
	MaxRequestBytes  int64
	MaxBodyBytes     int64
}

type Gmail struct {
	baseURL          *url.URL
	accessToken      string
	accountID        string
	client           *http.Client
	timeout          time.Duration
	maxResponseBytes int64
	maxRequestBytes  int64
	maxBodyBytes     int64
}

func NewGmail(config GmailConfig) (*Gmail, error) {
	base := strings.TrimSpace(config.BaseURL)
	if base == "" {
		base = defaultGmailBaseURL
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || strings.TrimSpace(config.AccessToken) == "" {
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
	accountID := strings.TrimSpace(config.AccountID)
	if accountID == "" {
		accountID = "me"
	}
	return &Gmail{
		baseURL: parsed, accessToken: strings.TrimSpace(config.AccessToken), accountID: accountID,
		client: &clone, timeout: timeout, maxResponseBytes: maxResponse,
		maxRequestBytes: maxRequest, maxBodyBytes: maxBody,
	}, nil
}

func (g *Gmail) endpoint(parts ...string) string {
	base := strings.TrimRight(g.baseURL.String(), "/")
	for _, part := range parts {
		base += "/" + url.PathEscape(part)
	}
	return base
}

func (g *Gmail) request(ctx context.Context, method, endpoint string, query url.Values, input, output any) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode gmail request: %w", err)
		}
		if int64(len(encoded)) > g.maxRequestBytes {
			return ErrBodyTooLarge
		}
		body = bytes.NewReader(encoded)
	}
	requestCtx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, method, endpoint, body)
	if err != nil {
		return fmt.Errorf("create gmail request: %w", err)
	}
	request.URL.RawQuery = query.Encode()
	request.Header.Set("Authorization", "Bearer "+g.accessToken)
	request.Header.Set("Accept", "application/json")
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := g.client.Do(request)
	if err != nil {
		return fmt.Errorf("gmail request: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, g.maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read gmail response: %w", err)
	}
	if int64(len(data)) > g.maxResponseBytes {
		return ErrResponseTooLarge
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return decodeGmailError(response.StatusCode, data)
	}
	if output == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, output); err != nil {
		return fmt.Errorf("decode gmail response: %w", err)
	}
	return nil
}

func decodeGmailError(status int, data []byte) error {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(data, &envelope)
	message := strings.TrimSpace(envelope.Error.Message)
	if message == "" {
		message = http.StatusText(status)
	}
	return &ProviderError{StatusCode: status, Message: message}
}

func (g *Gmail) Account(ctx context.Context) (Account, error) {
	var profile gmailProfile
	err := g.request(ctx, http.MethodGet, g.endpoint("users", "me", "profile"), nil, nil, &profile)
	if err != nil {
		return Account{}, err
	}
	if strings.TrimSpace(profile.EmailAddress) == "" {
		return Account{}, errors.New("gmail profile is missing email address")
	}
	return Account{
		Provenance: Provenance{Provider: ProviderGmail, ProviderID: profile.EmailAddress, AccountID: g.accountID},
		Email:      profile.EmailAddress, Total: profile.MessagesTotal,
	}, nil
}

func (g *Gmail) ListFolders(ctx context.Context) ([]Folder, error) {
	var response struct {
		Labels []gmailLabel `json:"labels"`
	}
	err := g.request(ctx, http.MethodGet, g.endpoint("users", "me", "labels"), nil, nil, &response)
	if err != nil {
		return nil, err
	}
	folders := make([]Folder, 0, len(response.Labels))
	for _, label := range response.Labels {
		if strings.TrimSpace(label.ID) == "" {
			continue
		}
		folders = append(folders, normalizeGmailFolder(g.accountID, label))
	}
	return folders, nil
}

func (g *Gmail) ListThreads(ctx context.Context, input ListThreadsRequest) (ThreadPage, error) {
	if input.PageSize < 0 || input.PageSize > 500 {
		return ThreadPage{}, ErrInvalidInput
	}
	query := make(url.Values)
	if input.PageSize > 0 {
		query.Set("maxResults", strconv.Itoa(input.PageSize))
	}
	if input.PageToken != "" {
		query.Set("pageToken", input.PageToken)
	}
	if input.Query != "" {
		query.Set("q", input.Query)
	}
	for _, label := range input.FolderIDs {
		if strings.TrimSpace(label) != "" {
			query.Add("labelIds", label)
		}
	}
	var response gmailThreadList
	if err := g.request(ctx, http.MethodGet, g.endpoint("users", "me", "threads"), query, nil, &response); err != nil {
		return ThreadPage{}, err
	}
	page := ThreadPage{NextPageToken: response.NextPageToken, EstimatedTotal: response.ResultSizeEstimate}

	validItems := make([]gmailThread, 0, len(response.Threads))
	for _, item := range response.Threads {
		if strings.TrimSpace(item.ID) != "" {
			validItems = append(validItems, item)
		}
	}

	page.Threads = make([]Thread, len(validItems))
	if len(validItems) == 0 {
		return page, nil
	}

	const maxConcurrency = 10
	semaphore := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup

	for index, item := range validItems {
		if len(item.Messages) > 0 {
			if norm, err := g.normalizeThread(item); err == nil {
				page.Threads[index] = norm
				continue
			}
		}

		wg.Add(1)
		go func(idx int, raw gmailThread) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			metaQuery := url.Values{
				"format":          []string{"metadata"},
				"metadataHeaders": []string{"Subject", "From", "To", "Cc", "Date"},
			}
			var detailed gmailThread
			if err := g.request(ctx, http.MethodGet, g.endpoint("users", "me", "threads", raw.ID), metaQuery, nil, &detailed); err == nil {
				if norm, err := g.normalizeThread(detailed); err == nil {
					page.Threads[idx] = norm
					return
				}
			}

			page.Threads[idx] = Thread{
				Provenance: Provenance{Provider: ProviderGmail, ProviderID: raw.ID, AccountID: g.accountID},
				Snippet:    cleanText(raw.Snippet),
			}
		}(index, item)
	}
	wg.Wait()

	return page, nil
}

func (g *Gmail) GetThread(ctx context.Context, id string) (Thread, error) {
	if strings.TrimSpace(id) == "" {
		return Thread{}, ErrInvalidInput
	}
	query := url.Values{"format": []string{"full"}}
	var response gmailThread
	if err := g.request(ctx, http.MethodGet, g.endpoint("users", "me", "threads", id), query, nil, &response); err != nil {
		return Thread{}, err
	}
	return g.normalizeThread(response)
}

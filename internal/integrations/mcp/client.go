package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	ErrCatalogLimit      = errors.New("mcp_catalog_limit")
	ErrUnsupportedResult = errors.New("mcp_unsupported_result")
	ErrRemoteTool        = errors.New("mcp_remote_tool_error")
)

type Tool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

type Discovery struct {
	ProtocolVersion string
	ServerName      string
	ServerVersion   string
	Tools           []Tool
}

type CallResult struct {
	Text              []string
	StructuredContent any
	IsError           bool
}

type ConnectorClient interface {
	Test(context.Context, string, string) error
	Discover(context.Context, string, string) (Discovery, error)
	CallTool(context.Context, string, string, string, json.RawMessage) (CallResult, error)
}

type Client struct {
	limits            Limits
	httpClientFactory func(string, string, Limits) (*http.Client, error)
}

func NewClient(limits Limits) *Client {
	return &Client{limits: limits.normalized(), httpClientFactory: NewHTTPClient}
}

func TestingNewClient(limits Limits, factory func(string, string, Limits) (*http.Client, error)) *Client {
	client := NewClient(limits)
	client.httpClientFactory = factory
	return client
}

func (c *Client) Test(ctx context.Context, endpoint, bearer string) error {
	_, err := c.Discover(ctx, endpoint, bearer)
	return err
}

func (c *Client) Discover(ctx context.Context, endpoint, bearer string) (Discovery, error) {
	session, cancel, err := c.connect(ctx, endpoint, bearer)
	if err != nil {
		return Discovery{}, err
	}
	defer cancel()
	defer session.Close()

	result := Discovery{}
	if initialized := session.InitializeResult(); initialized != nil {
		result.ProtocolVersion = initialized.ProtocolVersion
		if initialized.ServerInfo != nil {
			result.ServerName = initialized.ServerInfo.Name
			result.ServerVersion = initialized.ServerInfo.Version
		}
	}
	cursor := ""
	seenCursors := map[string]bool{}
	seenNames := map[string]bool{}
	for {
		page, listErr := session.ListTools(ctx, &mcpsdk.ListToolsParams{Cursor: cursor})
		if listErr != nil {
			return Discovery{}, listErr
		}
		for _, remote := range page.Tools {
			if remote == nil {
				continue
			}
			name := strings.TrimSpace(remote.Name)
			if name == "" || len(name) > c.limits.MaxToolNameBytes || seenNames[name] {
				return Discovery{}, ErrCatalogLimit
			}
			if len(result.Tools) >= c.limits.MaxCatalogTools || len(remote.Description) > 4000 {
				return Discovery{}, ErrCatalogLimit
			}
			schema, marshalErr := json.Marshal(remote.InputSchema)
			if marshalErr != nil {
				return Discovery{}, fmt.Errorf("marshal MCP tool schema: %w", marshalErr)
			}
			if int64(len(schema)) > c.limits.MaxRequestBytes {
				return Discovery{}, ErrCatalogLimit
			}
			seenNames[name] = true
			result.Tools = append(result.Tools, Tool{Name: name, Description: remote.Description, InputSchema: schema})
		}
		if page.NextCursor == "" {
			return result, nil
		}
		if seenCursors[page.NextCursor] {
			return Discovery{}, ErrCatalogLimit
		}
		seenCursors[page.NextCursor] = true
		cursor = page.NextCursor
	}
}

func (c *Client) CallTool(ctx context.Context, endpoint, bearer, name string, arguments json.RawMessage) (CallResult, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > c.limits.MaxToolNameBytes || len(arguments) > int(c.limits.MaxRequestBytes) {
		return CallResult{}, ErrUnsupportedResult
	}
	var input map[string]any
	if err := json.Unmarshal(arguments, &input); err != nil {
		return CallResult{}, err
	}
	if input == nil {
		return CallResult{}, ErrUnsupportedResult
	}
	session, cancel, err := c.connect(ctx, endpoint, bearer)
	if err != nil {
		return CallResult{}, err
	}
	defer cancel()
	defer session.Close()

	remote, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: name, Arguments: input})
	if err != nil {
		return CallResult{}, err
	}
	if remote.NeedsInput() || len(remote.InputRequests) > 0 {
		return CallResult{}, ErrUnsupportedResult
	}
	result := CallResult{StructuredContent: remote.StructuredContent, IsError: remote.IsError}
	for _, content := range remote.Content {
		text, ok := content.(*mcpsdk.TextContent)
		if !ok {
			return CallResult{}, ErrUnsupportedResult
		}
		result.Text = append(result.Text, text.Text)
	}
	encoded, err := json.Marshal(result)
	if err != nil || int64(len(encoded)) > c.limits.MaxResponseBytes {
		return CallResult{}, ErrUnsupportedResult
	}
	if result.IsError {
		return result, ErrRemoteTool
	}
	return result, nil
}

func (c *Client) connect(ctx context.Context, endpoint, bearer string) (*mcpsdk.ClientSession, context.CancelFunc, error) {
	httpClient, err := c.httpClientFactory(endpoint, bearer, c.limits)
	if err != nil {
		return nil, func() {}, err
	}
	requestCtx, cancel := context.WithTimeout(ctx, c.limits.RequestTimeout)
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "misty", Version: "1"}, &mcpsdk.ClientOptions{Capabilities: &mcpsdk.ClientCapabilities{}})
	transport := &mcpsdk.StreamableClientTransport{
		Endpoint:             endpoint,
		HTTPClient:           httpClient,
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}
	session, err := client.Connect(requestCtx, transport, nil)
	if err != nil {
		cancel()
		return nil, func() {}, err
	}
	return session, func() {
		cancel()
		if transport.HTTPClient != nil {
			transport.HTTPClient.CloseIdleConnections()
		}
	}, nil
}

func DefaultLimits() Limits {
	return Limits{
		ConnectTimeout:      5 * time.Second,
		TLSHandshakeTimeout: 5 * time.Second,
		RequestTimeout:      20 * time.Second,
		MaxRequestBytes:     512 << 10,
		MaxResponseBytes:    2 << 20,
		MaxCatalogTools:     200,
		MaxToolNameBytes:    240,
	}
}

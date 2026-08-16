package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	mcp "github.com/kannachi323/misty/server/internal/integrations/mcp"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type interoperabilityInput struct {
	Message string `json:"message" jsonschema:"message to echo"`
}

type interoperabilityOutput struct {
	Echo string `json:"echo" jsonschema:"echoed message"`
}

func interoperabilityEcho(_ context.Context, _ *mcpsdk.CallToolRequest, input interoperabilityInput) (*mcpsdk.CallToolResult, interoperabilityOutput, error) {
	return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "echo: " + input.Message}}}, interoperabilityOutput{Echo: input.Message}, nil
}

func officialSDKServer(t *testing.T, toolCount int, methods *[]string, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "official-test-server", Version: "1.2.3"}, nil)
	for _, name := range []string{"echo", "second"}[:toolCount] {
		mcpsdk.AddTool(server, &mcpsdk.Tool{Name: name, Description: "Echo one message."}, interoperabilityEcho)
	}
	official := mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server { return server }, &mcpsdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	handler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		request.Body = io.NopCloser(bytes.NewReader(body))
		var rpc struct {
			Method string `json:"method"`
			Params struct {
				Name string         `json:"name"`
				Meta map[string]any `json:"_meta"`
			} `json:"params"`
		}
		if json.Unmarshal(body, &rpc) != nil || rpc.Method == "" {
			t.Errorf("invalid JSON-RPC request: %s", body)
		}
		if request.Method != http.MethodPost || request.Header.Get("MCP-Protocol-Version") != "2026-07-28" || request.Header.Get("Mcp-Method") != rpc.Method {
			t.Errorf("invalid modern transport contract: method=%s headers=%#v", request.Method, request.Header)
		}
		if rpc.Method == "tools/call" && request.Header.Get("Mcp-Name") != rpc.Params.Name {
			t.Errorf("Mcp-Name=%q body name=%q", request.Header.Get("Mcp-Name"), rpc.Params.Name)
		}
		if rpc.Params.Meta["io.modelcontextprotocol/protocolVersion"] != "2026-07-28" {
			t.Errorf("missing modern per-request metadata: %#v", rpc.Params.Meta)
		}
		accept := request.Header.Get("Accept")
		if !strings.Contains(accept, "application/json") || !strings.Contains(accept, "text/event-stream") || request.Header.Get("Mcp-Session-Id") != "" {
			t.Errorf("invalid accept/session contract: accept=%q session=%q", accept, request.Header.Get("Mcp-Session-Id"))
		}
		mu.Lock()
		*methods = append(*methods, rpc.Method)
		mu.Unlock()
		official.ServeHTTP(response, request)
	})
	return httptest.NewTLSServer(handler)
}

func TestClientInteroperatesWithOfficial20260728SDK(t *testing.T) {
	var mu sync.Mutex
	methods := []string{}
	tlsServer := officialSDKServer(t, 1, &methods, &mu)
	defer tlsServer.Close()
	client := mcp.TestingNewClient(mcp.DefaultLimits(), func(string, string, mcp.Limits) (*http.Client, error) {
		return tlsServer.Client(), nil
	})
	discovery, err := client.Discover(t.Context(), tlsServer.URL, "")
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if discovery.ProtocolVersion != "2026-07-28" || discovery.ServerName != "official-test-server" || discovery.ServerVersion != "1.2.3" || len(discovery.Tools) != 1 || discovery.Tools[0].Name != "echo" || !json.Valid(discovery.Tools[0].InputSchema) {
		t.Fatalf("unexpected discovery: %#v", discovery)
	}
	result, err := client.CallTool(t.Context(), tlsServer.URL, "", "echo", json.RawMessage(`{"message":"Misty"}`))
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	structured, ok := result.StructuredContent.(map[string]any)
	if result.IsError || len(result.Text) != 1 || result.Text[0] != "echo: Misty" || !ok || structured["echo"] != "Misty" {
		t.Fatalf("unexpected result: %#v", result)
	}
	mu.Lock()
	gotMethods := append([]string(nil), methods...)
	mu.Unlock()
	if strings.Join(gotMethods, ",") != "server/discover,tools/list,server/discover,tools/call" {
		t.Fatalf("unexpected RPC sequence: %v", gotMethods)
	}
}

func TestClientRejectsCatalogBeyondConfiguredLimit(t *testing.T) {
	var mu sync.Mutex
	methods := []string{}
	tlsServer := officialSDKServer(t, 2, &methods, &mu)
	defer tlsServer.Close()
	limits := mcp.DefaultLimits()
	limits.MaxCatalogTools = 1
	client := mcp.TestingNewClient(limits, func(string, string, mcp.Limits) (*http.Client, error) {
		return tlsServer.Client(), nil
	})
	if _, err := client.Discover(t.Context(), tlsServer.URL, ""); !errors.Is(err, mcp.ErrCatalogLimit) {
		t.Fatalf("Discover() error = %v, want ErrCatalogLimit", err)
	}
}

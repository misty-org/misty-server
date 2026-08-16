package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	mcpintegration "github.com/kannachi323/misty/server/internal/integrations/mcp"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

const mcpSecretAAD = "misty-mcp-connector-v1"

var mcpToolSlugPattern = regexp.MustCompile(`[^a-z0-9]+`)

type mcpToolDTO struct {
	ConnectionID   string          `json:"connection_id"`
	RemoteName     string          `json:"remote_name"`
	StableName     string          `json:"stable_name"`
	Description    string          `json:"description"`
	InputSchema    json.RawMessage `json:"input_schema"`
	SchemaStatus   string          `json:"schema_status"`
	DisabledReason string          `json:"disabled_reason,omitempty"`
	DefaultRisk    string          `json:"default_risk"`
	Approval       string          `json:"approval"`
	Locality       string          `json:"locality"`
	DiscoveredAt   any             `json:"discovered_at"`
}

func mcpRemoteToolDTO(item db.MCPRemoteTool) mcpToolDTO {
	return mcpToolDTO{ConnectionID: item.ConnectionID, RemoteName: item.RemoteName, StableName: item.StableName, Description: item.Description, InputSchema: item.InputSchema, SchemaStatus: item.SchemaStatus, DisabledReason: item.DisabledReason, DefaultRisk: "write", Approval: "interactive", Locality: "provider", DiscoveredAt: item.DiscoveredAt}
}

func (s *SpacesService) MCPConnections() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if r.Method == http.MethodGet {
			items, err := s.database.MCPRemoteConnections(r.Context(), userID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"connections": items})
			return
		}
		var body struct {
			Name        string `json:"name"`
			EndpointURL string `json:"endpoint_url"`
			BearerToken string `json:"bearer_token"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		body.Name, body.EndpointURL = strings.TrimSpace(body.Name), strings.TrimSpace(body.EndpointURL)
		if !validMCPHTTPSURL(body.EndpointURL) || len(body.BearerToken) > 8192 || strings.ContainsAny(body.BearerToken, "\r\n") || strings.HasPrefix(strings.ToLower(body.BearerToken), "bearer ") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "mcp_invalid_connection"})
			return
		}
		ciphertext, nonce, err := s.encryptMCPBearer(body.BearerToken)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		item, err := s.database.CreateMCPRemoteConnection(r.Context(), db.MCPRemoteConnection{OwnerUserID: userID, Name: body.Name, EndpointURL: body.EndpointURL, BearerCiphertext: ciphertext, BearerNonce: nonce, KeyVersion: int(s.keyVer)})
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"connection": item})
	}
}

func (s *SpacesService) MCPConnection() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		connectionID := chi.URLParam(r, "connectionID")
		if r.Method == http.MethodDelete {
			if err := s.database.RevokeMCPRemoteConnection(r.Context(), userID, connectionID); err != nil {
				writeSpaceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		item, err := s.database.MCPRemoteConnection(r.Context(), userID, connectionID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"connection": item})
	}
}

func (s *SpacesService) TestMCPConnection() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		item, bearer, err := s.mcpConnectionAccess(r, userID, chi.URLParam(r, "connectionID"))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		err = s.mcpConnectorClient.Test(r.Context(), item.EndpointURL, bearer)
		if err != nil {
			item, _ = s.database.SetMCPConnectionHealth(r.Context(), userID, item.ID, "needs_attention", mcpErrorCode(err), false)
			writeJSON(w, http.StatusOK, map[string]any{"connection": item, "ok": false})
			return
		}
		item, err = s.database.SetMCPConnectionHealth(r.Context(), userID, item.ID, "active", "", false)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"connection": item, "ok": true})
	}
}

func (s *SpacesService) DiscoverMCPConnection() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		item, bearer, err := s.mcpConnectionAccess(r, userID, chi.URLParam(r, "connectionID"))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		discovery, err := s.mcpConnectorClient.Discover(r.Context(), item.EndpointURL, bearer)
		if err != nil {
			_, _ = s.database.SetMCPConnectionHealth(r.Context(), userID, item.ID, "needs_attention", mcpErrorCode(err), false)
			writeJSON(w, http.StatusBadGateway, map[string]string{"code": mcpErrorCode(err)})
			return
		}
		tools := normalizeMCPDiscovery(item.ID, discovery.Tools)
		fingerprint := mcpCatalogFingerprint(tools)
		snapshot, err := s.database.SaveMCPDiscovery(r.Context(), userID, db.MCPDiscoverySnapshot{ConnectionID: item.ID, ProtocolVersion: discovery.ProtocolVersion, ServerName: discovery.ServerName, ServerVersion: discovery.ServerVersion, CatalogFingerprint: fingerprint, ToolCount: len(tools), Status: "complete"}, tools)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		item, err = s.database.SetMCPConnectionHealth(r.Context(), userID, item.ID, "active", "", true)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		storedTools, err := s.database.MCPRemoteTools(r.Context(), userID, item.ID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		dtos := make([]mcpToolDTO, 0, len(storedTools))
		for _, tool := range storedTools {
			dtos = append(dtos, mcpRemoteToolDTO(tool))
		}
		writeJSON(w, http.StatusOK, map[string]any{"connection": item, "snapshot": snapshot, "tools": dtos})
	}
}

func (s *SpacesService) MCPConnectionTools() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		items, err := s.database.MCPRemoteTools(r.Context(), userID, chi.URLParam(r, "connectionID"))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		dtos := make([]mcpToolDTO, 0, len(items))
		for _, item := range items {
			dtos = append(dtos, mcpRemoteToolDTO(item))
		}
		writeJSON(w, http.StatusOK, map[string]any{"tools": dtos})
	}
}

func (s *SpacesService) PersonalAgentMCPTools() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		agentID := chi.URLParam(r, "agentID")
		if r.Method == http.MethodGet {
			items, err := s.database.PersonalAgentMCPTools(r.Context(), userID, agentID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"agent_id": agentID, "tools": mcpBindingDTOs(items)})
			return
		}
		var body struct {
			Tools []db.MCPAgentToolSelection `json:"tools"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		items, err := s.database.SetPersonalAgentMCPTools(r.Context(), userID, agentID, body.Tools)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"agent_id": agentID, "tools": mcpBindingDTOs(items)})
	}
}

func (s *SpacesService) PersonalAgentMCPExecutions() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit < 1 || limit > 100 {
			limit = 50
		}
		items, err := s.database.MCPExecutionAudits(r.Context(), userID, chi.URLParam(r, "agentID"), limit)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"executions": items})
	}
}

func (s *SpacesService) mcpConnectionAccess(r *http.Request, userID, connectionID string) (*db.MCPRemoteConnection, string, error) {
	item, err := s.database.MCPRemoteConnection(r.Context(), userID, connectionID)
	if err != nil {
		return nil, "", err
	}
	bearer, err := s.decryptMCPBearer(item.BearerCiphertext, item.BearerNonce)
	return item, bearer, err
}

func (s *SpacesService) encryptMCPBearer(value string) ([]byte, []byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	return s.aead.Seal(nil, nonce, []byte(value), []byte(mcpSecretAAD)), nonce, nil
}

func (s *SpacesService) decryptMCPBearer(ciphertext, nonce []byte) (string, error) {
	plaintext, err := s.aead.Open(nil, nonce, ciphertext, []byte(mcpSecretAAD))
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func validMCPHTTPSURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""
}

func normalizeMCPDiscovery(connectionID string, remote []mcpintegration.Tool) []db.MCPRemoteTool {
	items := make([]db.MCPRemoteTool, 0, len(remote))
	for _, tool := range remote {
		schema := tool.InputSchema
		status, reason := "valid", ""
		if err := validateMCPToolSchema(schema); err != nil {
			status, reason = "unsupported", "unsupported_input_schema"
		}
		hash := sha256.Sum256(schema)
		items = append(items, db.MCPRemoteTool{ConnectionID: connectionID, RemoteName: tool.Name, StableName: stableMCPToolName(connectionID, tool.Name), Description: tool.Description, InputSchema: schema, SchemaFingerprint: hex.EncodeToString(hash[:]), SchemaStatus: status, DisabledReason: reason})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].RemoteName < items[j].RemoteName })
	return items
}

func stableMCPToolName(connectionID, remoteName string) string {
	hash := sha256.Sum256([]byte(connectionID + "\x00" + remoteName))
	slug := strings.Trim(mcpToolSlugPattern.ReplaceAllString(strings.ToLower(remoteName), "_"), "_")
	if slug == "" {
		slug = "tool"
	}
	if len(slug) > 80 {
		slug = slug[:80]
	}
	return "mcp." + hex.EncodeToString(hash[:6]) + "." + slug
}

func mcpCatalogFingerprint(items []db.MCPRemoteTool) string {
	hash := sha256.New()
	for _, item := range items {
		_, _ = hash.Write([]byte(item.RemoteName + "\x00" + item.SchemaFingerprint + "\n"))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func validateMCPToolSchema(raw json.RawMessage) error {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return errors.New("invalid schema")
	}
	object, ok := value.(map[string]any)
	if !ok || object["type"] != "object" {
		return errors.New("root schema must be object")
	}
	return validateMCPToolSchemaNode(object)
}

func validateMCPToolSchemaNode(value any) error {
	object, ok := value.(map[string]any)
	if !ok {
		return errors.New("schema must be object")
	}
	allowed := map[string]bool{"type": true, "properties": true, "required": true, "additionalProperties": true, "items": true, "enum": true, "minItems": true, "maxItems": true, "minLength": true, "maxLength": true, "description": true, "title": true, "default": true, "$schema": true}
	for key := range object {
		if !allowed[key] {
			return errors.New("unsupported schema keyword")
		}
	}
	typeName, _ := object["type"].(string)
	if typeName == "" {
		typeName = "object"
	}
	if typeName != "object" && typeName != "array" && typeName != "string" && typeName != "number" && typeName != "integer" && typeName != "boolean" && typeName != "null" && typeName != "any" {
		return errors.New("unsupported schema type")
	}
	if properties, exists := object["properties"]; exists {
		values, ok := properties.(map[string]any)
		if !ok {
			return errors.New("invalid properties")
		}
		for _, child := range values {
			if err := validateMCPToolSchemaNode(child); err != nil {
				return err
			}
		}
	}
	if required, exists := object["required"]; exists {
		values, ok := required.([]any)
		if !ok {
			return errors.New("invalid required")
		}
		for _, value := range values {
			if _, ok := value.(string); !ok {
				return errors.New("invalid required")
			}
		}
	}
	if additional, exists := object["additionalProperties"]; exists {
		if _, ok := additional.(bool); !ok {
			return errors.New("unsupported additionalProperties")
		}
	}
	if items, exists := object["items"]; exists {
		if err := validateMCPToolSchemaNode(items); err != nil {
			return err
		}
	}
	return nil
}

func mcpBindingDTOs(items []db.MCPAgentToolBinding) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{"connection_id": item.ConnectionID, "connection_name": item.ConnectionName, "remote_name": item.RemoteName, "stable_name": item.StableName, "description": item.Description, "input_schema": item.InputSchema, "schema_status": item.SchemaStatus, "disabled_reason": item.DisabledReason, "enabled": item.Enabled, "default_risk": "write", "approval": "interactive", "locality": "provider"})
	}
	return out
}

func mcpErrorCode(err error) string {
	switch {
	case errors.Is(err, mcpintegration.ErrCatalogLimit):
		return "mcp_catalog_rejected"
	case errors.Is(err, mcpintegration.ErrUnsupportedResult):
		return "mcp_result_unsupported"
	case errors.Is(err, mcpintegration.ErrRemoteTool):
		return "mcp_remote_tool_error"
	default:
		return "mcp_unavailable"
	}
}

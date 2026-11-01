package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"

	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/agenttools"
)

func validBrowserExtractRequest(raw json.RawMessage) (string, json.RawMessage, error) {
	var input struct {
		Instruction string          `json:"instruction"`
		Schema      json.RawMessage `json:"schema"`
	}
	if json.Unmarshal(raw, &input) != nil || strings.TrimSpace(input.Instruction) == "" || len(input.Instruction) > 4000 {
		return "", nil, agent.ErrInvalidRequest("browser_extract needs an instruction")
	}
	var schema map[string]any
	if len(input.Schema) == 0 || len(input.Schema) > browserExtractSchemaLimit || json.Unmarshal(input.Schema, &schema) != nil || schema["type"] != "object" {
		return "", nil, agent.ErrInvalidRequest("browser_extract needs a JSON Schema whose top level has type object")
	}
	return input.Instruction, input.Schema, nil
}

// browserExtract checks and returns what the desktop extracted from the page it
// read, on the device's agent model (docs/agents/runtime.md). A job that comes
// back without data is a tool error: the server never runs the extraction.
func (s *SpacesService) browserExtract(ctx context.Context, invocation agenttools.Invocation, _ string, arguments, page json.RawMessage) (out json.RawMessage, err error) {
	// The model sees a generic failure; the reason belongs in the server log.
	defer func() {
		if err != nil {
			log.Printf("browser.extract failed for run %s: %v", invocation.RunID, err)
		}
	}()
	if _, _, err = validBrowserExtractRequest(arguments); err != nil {
		return nil, err
	}
	var source struct {
		URL       string          `json:"url"`
		Title     string          `json:"title"`
		Markdown  string          `json:"markdown"`
		Truncated bool            `json:"truncated"`
		Data      json.RawMessage `json:"data"`
	}
	if json.Unmarshal(page, &source) != nil {
		return nil, errBrowserObservationFailed
	}
	if len(source.Markdown) > browserExtractPageLimit {
		source.Markdown = source.Markdown[:browserExtractPageLimit]
		source.Truncated = true
	}
	if len(source.Data) == 0 {
		return nil, errors.Join(errBrowserObservationFailed, errors.New("the desktop sent no extraction"))
	}
	if !json.Valid(source.Data) || string(source.Data) == "null" {
		return nil, errors.Join(errBrowserObservationFailed, errors.New("the extraction did not match the schema"))
	}
	return extractionReply(source.Data, source.Markdown, source.URL, source.Title, source.Truncated), nil
}

// extractionReply is browser.extract's result. Links come from the page, never from the
// model's copy of them (browser_extract_links.go).
func extractionReply(data json.RawMessage, markdown, pageURL, title string, truncated bool) json.RawMessage {
	data, corrected, removed := verifyExtractedLinks(data, markdown, pageURL)
	reply := map[string]any{
		"data": data, "url": pageURL, "title": title, "pageTruncated": truncated, "contentTrust": "untrusted-web-page",
	}
	if corrected > 0 {
		reply["linksCorrected"] = corrected
	}
	if len(removed) > 0 {
		reply["linksRemoved"] = removed
		reply["note"] = "Links the page does not hold were removed; open listings by clicking them on the page."
	}
	return MustAPIRawJSON(reply)
}

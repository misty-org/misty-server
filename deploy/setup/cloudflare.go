package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// cloudflare is a minimal client for the Cloudflare v4 API. Errors name the
// request and Cloudflare's error codes, never the token or response bodies.
type cloudflare struct {
	base    string
	token   string
	account string
	http    *http.Client
}

type apiError struct {
	Method, Path string
	Status       int
	Codes        []int
}

func (e *apiError) Error() string {
	return fmt.Sprintf("Cloudflare refused %s %s (HTTP %d, codes %v); check the API token's permissions", e.Method, e.Path, e.Status, e.Codes)
}

func notFound(err error) bool {
	var api *apiError
	return errors.As(err, &api) && api.Status == http.StatusNotFound
}

func newCloudflare(inputs Inputs) *cloudflare {
	return &cloudflare{base: inputs.APIURL, token: inputs.Token, account: inputs.AccountID, http: &http.Client{Timeout: 60 * time.Second}}
}

// call sends a JSON request (or nil) and decodes the envelope's result into out.
func (c *cloudflare) call(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	contentType := ""
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader, contentType = bytes.NewReader(data), "application/json"
	}
	return c.send(ctx, method, path, reader, contentType, out)
}

func (c *cloudflare) send(ctx context.Context, method, path string, body io.Reader, contentType string, out any) error {
	request, err := http.NewRequestWithContext(ctx, method, c.base+"/"+strings.TrimLeft(path, "/"), body)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("Cloudflare %s %s failed: %w", method, redactPath(path), errors.Unwrap(err))
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if err != nil {
		return err
	}
	var envelope struct {
		Success bool                 `json:"success"`
		Errors  []struct{ Code int } `json:"errors"`
		Result  json.RawMessage      `json:"result"`
	}
	decodeErr := json.Unmarshal(data, &envelope)
	if response.StatusCode >= 300 || decodeErr != nil || !envelope.Success {
		failure := &apiError{Method: method, Path: redactPath(path), Status: response.StatusCode}
		for _, item := range envelope.Errors {
			failure.Codes = append(failure.Codes, item.Code)
		}
		return failure
	}
	if out == nil || len(envelope.Result) == 0 || string(envelope.Result) == "null" {
		return nil
	}
	if err := json.Unmarshal(envelope.Result, out); err != nil {
		return fmt.Errorf("Cloudflare %s %s returned an unexpected result: %w", method, redactPath(path), err)
	}
	return nil
}

// redactPath drops the query, which can carry names but never credentials,
// to keep messages short.
func redactPath(path string) string {
	if index := strings.IndexByte(path, '?'); index >= 0 {
		return path[:index]
	}
	return path
}

func (c *cloudflare) accountPath(format string, args ...any) string {
	return "accounts/" + c.account + "/" + fmt.Sprintf(format, args...)
}

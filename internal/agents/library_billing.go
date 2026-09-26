package agent

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/kannachi323/misty/server/internal/billingadapter"
	"github.com/kannachi323/misty/server/internal/library"
)

func (a *SmartLibraryAnalyzer) WithBilling(service *billingadapter.Service, account, operation string) *SmartLibraryAnalyzer {
	clone := *a
	clone.billing = &library.Meter{Service: service, Account: account, OperationID: operation}
	return &clone
}

func (a *SmartLibraryAnalyzer) BillingError() error { return a.billing.Err() }

func (a *SmartLibraryAnalyzer) beginLibraryRequest(ctx context.Context, url string, payload []byte, headers map[string]string) (*library.Attempt, error) {
	if a.billing == nil {
		return nil, nil
	}
	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		return nil, err
	}
	model, _ := body["model"].(string)
	if model == "" {
		model = headers["ai-model-id"]
	}
	operation := "library.model"
	units := map[string]int64{"input_tokens": max(int64(1), libraryInputBound(body))}
	if strings.HasSuffix(url, "/embeddings") || strings.HasSuffix(url, "/embedding-model") {
		operation = "library.embedding"
	} else if n, ok := body["max_tokens"].(float64); ok {
		units["output_tokens"] = int64(n)
	}
	return a.billing.Begin(ctx, operation, model, units)
}

// Text bytes provide a conservative token estimate. Image data is bounded as
// image input, not billed as millions of base64 text characters.
func libraryInputBound(value any) int64 {
	switch v := value.(type) {
	case string:
		if strings.HasPrefix(v, "data:image/") {
			return 8192
		}
		return int64(len(v))
	case []any:
		var n int64
		for _, item := range v {
			n += libraryInputBound(item)
		}
		return n
	case map[string]any:
		var n int64
		for key, item := range v {
			if key != "model" {
				n += libraryInputBound(item)
			}
		}
		return n
	}
	return 0
}

func libraryResponseUsage(raw []byte) (map[string]int64, bool) {
	var response struct {
		Usage struct {
			Prompt       *int64 `json:"prompt_tokens"`
			Input        *int64 `json:"input_tokens"`
			Tokens       *int64 `json:"tokens"`
			Output       *int64 `json:"completion_tokens"`
			OutputTokens *int64 `json:"output_tokens"`
			Details      struct {
				Cached int64 `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if json.Unmarshal(raw, &response) != nil {
		return nil, true
	}
	u := response.Usage
	input := u.Prompt
	if input == nil {
		input = u.Input
	}
	if input == nil {
		input = u.Tokens
	}
	output := u.Output
	if output == nil {
		output = u.OutputTokens
	}
	if input == nil {
		return nil, true
	}
	units := map[string]int64{"input_tokens": *input, "cached_input_tokens": u.Details.Cached}
	if output != nil {
		units["output_tokens"] = *output
	}
	for _, n := range units {
		if n < 0 {
			return nil, true
		}
	}
	if u.Details.Cached > *input {
		return nil, true
	}
	return units, false
}

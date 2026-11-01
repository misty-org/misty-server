package api

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"math"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestDeviceQueryVectorsComeFromTheDesktop(t *testing.T) {
	raw := make([]byte, deviceEmbeddingDimensions*4)
	for i := 0; i < deviceEmbeddingDimensions; i++ {
		binary.LittleEndian.PutUint32(raw[i*4:], math.Float32bits(float32(i)/1000))
	}
	request := httptest.NewRequest("GET", "/me/memory/events?q=aisle", nil)
	request.Header.Set("X-Misty-Query-Embedding", base64.StdEncoding.EncodeToString(raw))
	request.Header.Set("X-Misty-Query-Embedding-Model", "ollama/nomic-embed-text")
	device, ok := deviceQueryEmbeddingFrom(withHeaderQueryEmbedding(request))
	if !ok || device.model != "ollama/nomic-embed-text" || len(device.vector) != deviceEmbeddingDimensions || math.Abs(device.vector[500]-0.5) > 1e-6 {
		t.Fatal("the Recall vector was not read", ok, device.model)
	}
	if queryEmbeddingModel(withHeaderQueryEmbedding(request)) != "ollama/nomic-embed-text" {
		t.Fatal("search did not use the device's embedding model")
	}
	short := httptest.NewRequest("GET", "/", nil)
	short.Header.Set("X-Misty-Query-Embedding", base64.StdEncoding.EncodeToString(raw[:40]))
	short.Header.Set("X-Misty-Query-Embedding-Model", "m")
	if _, ok := deviceQueryEmbeddingFrom(withHeaderQueryEmbedding(short)); ok {
		t.Fatal("a vector of the wrong size was kept")
	}

	values := make([]any, deviceEmbeddingDimensions)
	for i := range values {
		values[i] = 0.25
	}
	call := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Meta: mcp.Meta{"misty/query_embedding": values, "misty/query_embedding_model": "openai/text-embedding-3-small"}}}
	device, ok = deviceQueryEmbeddingFrom(withDeviceQueryEmbedding(context.Background(), call))
	if !ok || device.vector[0] != 0.25 || device.model != "openai/text-embedding-3-small" {
		t.Fatal("the tool call's vector was not read")
	}
	if _, ok := deviceQueryEmbeddingFrom(withDeviceQueryEmbedding(context.Background(), &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{}})); ok {
		t.Fatal("a call with no vector got one")
	}
}

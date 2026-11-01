package api

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"math"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A desktop runtime embeds an agent's search query on the device's embedding
// model and sends the vector with the tool call (docs/agents/runtime.md),
// so the server ranks by meaning without calling a model.
type deviceQueryEmbeddingKey struct{}

type deviceQueryEmbedding struct {
	vector []float64
	model  string
}

// withDeviceQueryEmbedding keeps the vector a tool call carries in
// misty/query_embedding, with its model in misty/query_embedding_model.
func withDeviceQueryEmbedding(ctx context.Context, request *mcp.CallToolRequest) context.Context {
	if request == nil || request.Params == nil {
		return ctx
	}
	values, _ := request.Params.Meta["misty/query_embedding"].([]any)
	model := mcpMetaString(request.Params.Meta, "misty/query_embedding_model", "")
	if len(values) != deviceEmbeddingDimensions || model == "" {
		return ctx
	}
	vector := make([]float64, len(values))
	for i, value := range values {
		number, ok := value.(float64)
		if !ok {
			return ctx
		}
		vector[i] = number
	}
	return context.WithValue(ctx, deviceQueryEmbeddingKey{}, deviceQueryEmbedding{vector: vector, model: model})
}

func deviceQueryEmbeddingFrom(ctx context.Context) (deviceQueryEmbedding, bool) {
	embedding, ok := ctx.Value(deviceQueryEmbeddingKey{}).(deviceQueryEmbedding)
	return embedding, ok
}

// agentQueryEmbedding is the vector the desktop sent for the text an agent
// searches its memory and the person's Misty content with. Without one there
// is no vector: the server never embeds a query itself.
func agentQueryEmbedding(ctx context.Context, query string) ([]float64, bool) {
	normalized := strings.ToLower(strings.Join(strings.Fields(query), " "))
	device, ok := deviceQueryEmbeddingFrom(ctx)
	if !ok || len([]rune(normalized)) < 3 {
		return nil, false
	}
	return device.vector, true
}

// queryEmbeddingModel is the model the desktop embedded the query on, or "".
func queryEmbeddingModel(ctx context.Context) string {
	device, _ := deviceQueryEmbeddingFrom(ctx)
	return device.model
}

// withHeaderQueryEmbedding keeps the vector the desktop made for what the person typed in
// Recall: X-Misty-Query-Embedding is base64 of 768 little-endian float32s, with its model in
// X-Misty-Query-Embedding-Model.
func withHeaderQueryEmbedding(r *http.Request) context.Context {
	model := strings.TrimSpace(r.Header.Get("X-Misty-Query-Embedding-Model"))
	raw, err := base64.StdEncoding.DecodeString(r.Header.Get("X-Misty-Query-Embedding"))
	if err != nil || model == "" || len(model) > 200 || len(raw) != deviceEmbeddingDimensions*4 {
		return r.Context()
	}
	vector := make([]float64, deviceEmbeddingDimensions)
	for i := range vector {
		vector[i] = float64(math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:])))
	}
	return context.WithValue(r.Context(), deviceQueryEmbeddingKey{}, deviceQueryEmbedding{vector: vector, model: model})
}

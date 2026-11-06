package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/misty-org/misty-server/internal/platform/db"
)

// A conversation keeps what its tools returned, readable by its owner and by
// whoever runs the server (docs/agents/README.md, Where conversations live). What
// never gets stored readable: results already marked redacted, which stay sealed,
// and any field named like a credential. Screenshots go to the object store, and
// the stored output names them.

// maxStoredToolOutputBytes bounds one call's stored output, after its images are out.
const maxStoredToolOutputBytes = 256 << 10

// maxStoredToolOutputStringRunes bounds one value in it. A page's text fits.
const maxStoredToolOutputStringRunes = 128_000

var errToolOutputStoreUnavailable = errors.New("screenshot can't be stored")

var toolOutputImage = regexp.MustCompile(`^data:(image/(?:png|jpeg|webp));base64,([A-Za-z0-9+/=]+)$`)

// toolResultSealed reports a result that's marked redacted: an app's result is held
// only as ciphertext for replay (apps_execute.go), and a tool can mark its own.
func toolResultSealed(name string, result json.RawMessage) bool {
	if name == appsExecuteTool {
		return true
	}
	var marked struct {
		Redacted bool `json:"redacted"`
		Sealed   bool `json:"sealed"`
	}
	return json.Unmarshal(result, &marked) == nil && (marked.Redacted || marked.Sealed)
}

// storedToolOutput turns a tool's value into what's stored: fields named like a
// credential are replaced, long strings cut, and each image handed to keepImage,
// whose answer stands in for it.
func storedToolOutput(value any, key string, keepImage func(mime string, data []byte) any) any {
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "-", "_"), " ", "_"))
	for _, sensitive := range []string{"authorization", "cookie", "credential", "password", "secret", "token", "api_key", "apikey"} {
		if strings.Contains(normalized, sensitive) {
			return "[redacted]"
		}
	}
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for childKey, child := range typed {
			out[childKey] = storedToolOutput(child, childKey, keepImage)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, child := range typed {
			out = append(out, storedToolOutput(child, key, keepImage))
		}
		return out
	case string:
		if match := toolOutputImage.FindStringSubmatch(typed); match != nil {
			data, err := base64.StdEncoding.DecodeString(match[2])
			if err != nil {
				return map[string]any{"screenshot": "not_stored"}
			}
			return keepImage(match[1], data)
		}
		runes := []rune(typed)
		if len(runes) > maxStoredToolOutputStringRunes {
			return string(runes[:maxStoredToolOutputStringRunes]) + "…"
		}
		return typed
	default:
		return typed
	}
}

// recordAIInvocationToolOutput stores one finished call in the background: storing
// it never holds up the agent, and a failure leaves the run alone.
func (s *SpacesService) recordAIInvocationToolOutput(ctx context.Context, record *db.AIInvocationRecord, call agentRuntimeToolCall, result json.RawMessage) {
	if s.database == nil || record == nil || record.ConversationID == "" || len(result) == 0 {
		return
	}
	copied := *record
	background, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	go func() {
		defer cancel()
		if err := s.storeAIInvocationToolOutput(background, &copied, call, result); err != nil {
			log.Printf("store tool output for %s: %v", copied.ID, err)
		}
	}()
}

func (s *SpacesService) storeAIInvocationToolOutput(ctx context.Context, record *db.AIInvocationRecord, call agentRuntimeToolCall, result json.RawMessage) error {
	output := db.AIToolOutput{InvocationID: record.ID, ToolCallID: call.CallID, ToolName: call.Name}
	if toolResultSealed(call.Name, result) {
		output.Sealed = true
		return s.database.RecordAIToolOutput(ctx, record.UserID, record.ConversationID, output)
	}
	var value any
	if json.Unmarshal(result, &value) != nil {
		return nil
	}
	stored := storedToolOutput(value, "", func(mime string, data []byte) any {
		id, err := s.storeToolScreenshot(ctx, record, mime, data)
		if err != nil {
			log.Printf("store tool screenshot for %s: %v", record.ID, err)
			return map[string]any{"screenshot": "not_stored"}
		}
		return map[string]any{"screenshot": id}
	})
	encoded, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	if len(encoded) > maxStoredToolOutputBytes {
		output.Truncated = true
		encoded, err = json.Marshal(string(encoded[:maxStoredToolOutputBytes]))
		if err != nil {
			return err
		}
	}
	output.Output = encoded
	return s.database.RecordAIToolOutput(ctx, record.UserID, record.ConversationID, output)
}

// storeToolScreenshot puts one image in the object store, where Library files and
// attachments live, and records it with the conversation.
func (s *SpacesService) storeToolScreenshot(ctx context.Context, record *db.AIInvocationRecord, mime string, data []byte) (string, error) {
	if s.toolOutputStore == nil {
		return "", errToolOutputStoreUnavailable
	}
	if len(data) == 0 || len(data) > int(maxMistyAttachmentBytes) {
		return "", errToolOutputStoreUnavailable
	}
	// Size is only metadata: a format the server can't read still gets kept.
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 {
		config = image.Config{Width: 1, Height: 1}
	}
	sum := sha256.Sum256(data)
	checksum := hex.EncodeToString(sum[:])
	key := "library/tool_" + uuid.NewString()
	if err := s.toolOutputStore.Put(ctx, key, bytes.NewReader(data), LibraryObjectMetadata{ByteSize: int64(len(data)), SHA256: checksum, MIMEType: mime}); err != nil {
		return "", err
	}
	id, err := s.database.CreateToolOutputAttachment(ctx, db.AIConversationAttachment{
		UserID: record.UserID, ConversationID: record.ConversationID, InvocationID: record.ID, DisplayName: "Screenshot",
		MIMEType: mime, ByteSize: int64(len(data)), SHA256: checksum, Width: config.Width, Height: config.Height, ObjectKey: key,
	})
	if err != nil {
		_ = s.toolOutputStore.Delete(ctx, key)
		return "", err
	}
	return id, nil
}

// TestingStoreAIToolOutput stores a call's output now, not in the background.
func (s *SpacesService) TestingStoreAIToolOutput(ctx context.Context, record *db.AIInvocationRecord, callID, name string, result json.RawMessage) error {
	return s.storeAIInvocationToolOutput(ctx, record, agentRuntimeToolCall{CallID: callID, Name: name}, result)
}

package agent

import (
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/agent"
)

// The guard on MaxProviderRequestBytes and the credit reservation both read
// requestSizeBytes, so Space context has to be counted or large contexts bypass
// the friendly error and are under-billed.
func TestRequestSizeBytesCountsPromptAndSpaceContext(t *testing.T) {
	base := RequestSizeBytes(ModelRequest{})
	withPrompt := RequestSizeBytes(ModelRequest{SystemPrompt: strings.Repeat("p", 100)})
	withCard := RequestSizeBytes(ModelRequest{SpaceCard: strings.Repeat("c", 100)})
	withRecords := RequestSizeBytes(ModelRequest{SpaceRecords: strings.Repeat("r", 100)})

	for name, got := range map[string]int{
		"system prompt": withPrompt,
		"space card":    withCard,
		"space records": withRecords,
	} {
		if got != base+100 {
			t.Fatalf("requestSizeBytes() ignored the %s: got %d, want %d", name, got, base+100)
		}
	}
}

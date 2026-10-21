package api

import (
	"strings"
	"testing"
)

func TestDeviceFailureCodesReachTheModelWithANextStep(t *testing.T) {
	if code := deviceFailureCode("agent tool failed: browser device tool failed: browser_timeout"); code != "browser_timeout" {
		t.Fatalf("code = %q", code)
	}
	if code := deviceFailureCode("some other failure"); code != "" {
		t.Fatalf("unrelated error read as a device failure: %q", code)
	}
	if message := deviceFailureMessage("browser_tab_closed"); !strings.HasPrefix(message, "browser_tab_closed: ") || !strings.Contains(message, "open the page again") {
		t.Fatalf("message = %q", message)
	}
	if message := deviceFailureMessage("something_new"); !strings.Contains(message, "try another way") {
		t.Fatalf("unknown code message = %q", message)
	}
}

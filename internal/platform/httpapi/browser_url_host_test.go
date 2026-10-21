package api

import (
	"context"
	"strings"
	"testing"
)

func TestAddressWithoutHostOnlyRejectsMissingHosts(t *testing.T) {
	err := addressWithoutHost(context.Background(), "https://no-such-host-misty-eval.invalid/cocktail")
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing host not reported: %v", err)
	}
	for _, address := range []string{"http://127.0.0.1:8080/", "http://router.lan/", "http://intranet/", "not a url"} {
		if err := addressWithoutHost(context.Background(), address); err != nil {
			t.Fatalf("%s rejected: %v", address, err)
		}
	}
}

package api

import (
	"encoding/json"
	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"

	mailintegration "github.com/kannachi323/misty/server/internal/integrations/mail"
)

func TestNativeMailThreadFixtures(t *testing.T) {
	const path = "../../../../docs/migration/fixtures/mail-threads.json"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name     string          `json:"name"`
		Provider string          `json:"provider"`
		ThreadID string          `json:"threadId"`
		Raw      json.RawMessage `json:"raw"`
		Expected json.RawMessage `json:"expected"`
	}
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	update := envconfig.Getenv("MISTY_UPDATE_MAIL_FIXTURES") == "1"
	for index := range fixtures {
		fixture := &fixtures[index]
		t.Run(fixture.Name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("unexpected mutation %s", r.Method)
					w.WriteHeader(405)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(fixture.Raw)
			}))
			defer server.Close()
			var provider mailintegration.Provider
			var err error
			if fixture.Provider == "google" {
				provider, err = mailintegration.NewGmail(mailintegration.GmailConfig{BaseURL: server.URL, AccessToken: "fixture-only", AccountID: "fixture-account"})
			} else {
				provider, err = mailintegration.NewOutlook(mailintegration.OutlookConfig{BaseURL: server.URL, AccessToken: "fixture-only", AccountID: "fixture-account"})
			}
			if err != nil {
				t.Fatal(err)
			}
			thread, err := provider.GetThread(t.Context(), fixture.ThreadID)
			if err != nil {
				t.Fatal(err)
			}
			actual, err := json.Marshal(TestingMailThreadToDTO(thread))
			if err != nil {
				t.Fatal(err)
			}
			if update {
				fixture.Expected = actual
				return
			}
			var want, got any
			if err := json.Unmarshal(fixture.Expected, &want); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(actual, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(want, got) {
				t.Fatal("Go mail behavior changed; review the shared fixture")
			}
		})
	}
	if update {
		encoded, err := json.MarshalIndent(fixtures, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(encoded, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

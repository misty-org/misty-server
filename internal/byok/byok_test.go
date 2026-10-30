package byok

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func encode(json string) string { return base64.RawURLEncoding.EncodeToString([]byte(json)) }

const sample = `{"version":1,"providers":{"anthropic":{"apiKey":"sk-ant-test"},"ollama":{"baseURL":"http://127.0.0.1:11434"},"openai-compatible":{"apiKey":"k"},"bogus":{"apiKey":"x"}},
 "models":{"agent":"anthropic/claude-sonnet-5-5","fast":"ollama/qwen3:14b","embedding":"gemini/text-embedding"},"reasoning":"HIGH"}`

func TestDecodeReadsTheHeader(t *testing.T) {
	c, err := Decode(encode(sample))
	if err != nil {
		t.Fatal(err)
	}
	if c.Models.Agent != "anthropic/claude-sonnet-5-5" || c.Reasoning != "high" {
		t.Fatalf("decoded = %+v", c)
	}
	if _, ok := c.Providers["bogus"]; ok {
		t.Fatal("an unknown provider was kept")
	}
	if _, ok := c.Providers["openai-compatible"]; ok {
		t.Fatal("openai-compatible without a baseURL was kept")
	}
	if credential, ok := c.CredentialFor("anthropic/claude-sonnet-5-5"); !ok || credential.APIKey != "sk-ant-test" || credential.Provider != "anthropic" {
		t.Fatalf("anthropic credential = %+v %v", credential, ok)
	}
	if credential, ok := c.CredentialFor("ollama/qwen3:14b"); !ok || credential.BaseURL == "" {
		t.Fatalf("ollama runs without a key: %+v %v", credential, ok)
	}
	if !c.Usable() {
		t.Fatal("an agent model with its key is usable")
	}
	// Padding is accepted too.
	if _, err := Decode(base64.URLEncoding.EncodeToString([]byte(sample))); err != nil {
		t.Fatal("padded base64url refused:", err)
	}
}

func TestDecodeRefusesMalformedHeaders(t *testing.T) {
	for name, value := range map[string]string{
		"empty":         "",
		"not base64":    "%%%",
		"not json":      encode("{"),
		"wrong version": encode(`{"version":2,"models":{"agent":"openai/gpt"}}`),
		"bad model":     encode(`{"version":1,"models":{"agent":"no-slash"}}`),
		"unknown model": encode(`{"version":1,"models":{"agent":"acme/model"}}`),
		"too large":     strings.Repeat("A", MaxHeaderBytes+1),
		"bad base url":  encode(`{"version":1,"providers":{"openai":{"apiKey":"k","baseURL":"file:///etc"}}}`),
	} {
		if _, err := Decode(value); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestRoleModelsFollowTheContract(t *testing.T) {
	c := &Credentials{Models: Models{Agent: "anthropic/a", Fast: "openai/f", Embedding: "google/e", Transcription: "soniox/t", Speech: "elevenlabs/s"}}
	for role, want := range map[string]string{
		"agent": "anthropic/a", "vision": "anthropic/a", "browsing": "anthropic/a",
		"routing": "openai/f", "dictation-cleanup": "openai/f", "voice-notes": "openai/f", "day-summary": "openai/f",
		"embedding": "google/e", "transcription": "soniox/t", "live-transcription": "soniox/t", "speech": "elevenlabs/s",
	} {
		if got := c.RoleModel(role); got != want {
			t.Errorf("%s = %q, want %q", role, got, want)
		}
	}
	c.Models.Fast = ""
	if c.RoleModel("routing") != "anthropic/a" {
		t.Fatal("fast does not default to the agent model")
	}
	c.Models.Embedding = ""
	if c.RoleModel("embedding") != "" {
		t.Fatal("embeddings have no fallback")
	}
	if ProviderOf("nim/moonshotai/kimi") != "nvidia" || ProviderOf("gemini/x") != "google" || ProviderOf("openrouter/anthropic/claude") != "openrouter" {
		t.Fatal("aliases")
	}
}

func TestStoreExpiresAfterTTLAndStaysBounded(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	store := TestingStore(func() time.Time { return now }, 2)
	c := &Credentials{Version: 1, Models: Models{Agent: "openai/x"}}
	store.Put("a", c)
	if store.Get("a") != c {
		t.Fatal("stored credentials missing")
	}
	now = now.Add(TTL - time.Second)
	if store.Get("a") == nil {
		t.Fatal("expired early")
	}
	now = now.Add(2 * time.Second)
	if store.Get("a") != nil {
		t.Fatal("kept past the TTL")
	}
	store.Put("a", c)
	now = now.Add(time.Minute)
	store.Put("b", c)
	now = now.Add(time.Minute)
	store.Put("c", c)
	if store.Len() != 2 || store.Get("a") != nil || store.Get("c") == nil {
		t.Fatalf("bounded store kept %d, a=%v", store.Len(), store.Get("a"))
	}
	store.Forget("c")
	if store.Get("c") != nil {
		t.Fatal("forget kept credentials")
	}
}

func TestMiddlewareStoresSignedInAccountsAndStripsTheHeader(t *testing.T) {
	store := NewStore()
	var seen *Credentials
	var header string
	handler := store.Middleware(func(r *http.Request) string { return r.Header.Get("X-Test-User") })(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen, header = store.For(r.Context(), "user-1"), r.Header.Get(Header)
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(Header, encode(sample))
	request.Header.Set("X-Test-User", "user-1")
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if seen == nil || seen.Models.Agent != "anthropic/claude-sonnet-5-5" || header != "" {
		t.Fatalf("seen = %+v, header = %q", seen, header)
	}
	if store.Get("user-1") == nil {
		t.Fatal("not stored for background work")
	}
	// A request without an account stores nothing; a malformed header is ignored.
	anonymous := httptest.NewRequest(http.MethodGet, "/", nil)
	anonymous.Header.Set(Header, encode(sample))
	handler.ServeHTTP(httptest.NewRecorder(), anonymous)
	bad := httptest.NewRequest(http.MethodGet, "/", nil)
	bad.Header.Set(Header, "%%%")
	bad.Header.Set("X-Test-User", "user-2")
	handler.ServeHTTP(httptest.NewRecorder(), bad)
	if store.Len() != 1 {
		t.Fatalf("store holds %d accounts", store.Len())
	}
	// Another account's request never reads this one's context copy.
	ctx := WithCredentials(context.Background(), seen)
	if store.For(ctx, "user-3") != nil {
		t.Fatal("credentials crossed accounts")
	}
}

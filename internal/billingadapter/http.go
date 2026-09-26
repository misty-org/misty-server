package billingadapter

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Mode              string
	URL               string
	Secret            string
	Hosted            bool
	AllowLoopbackHTTP bool
}

// New requires explicit HTTP billing for hosted deployments. Self-hosted servers
// may omit billing altogether. A mistyped mode never grants access implicitly.
func New(c Config) (Adapter, error) {
	mode := strings.ToLower(strings.TrimSpace(c.Mode))
	if mode == "" || mode == "none" || mode == "null" {
		if c.Hosted {
			return nil, fmt.Errorf("hosted deployment requires an explicit http billing adapter")
		}
		return Disabled{}, nil
	}
	if mode != "http" {
		return nil, fmt.Errorf("unknown billing adapter mode %q", mode)
	}
	u, err := url.Parse(c.URL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("invalid billing adapter URL")
	}
	loopback := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"
	if u.Scheme != "https" && !(u.Scheme == "http" && c.AllowLoopbackHTTP && loopback) {
		return nil, fmt.Errorf("billing adapter requires HTTPS")
	}
	if len(c.Secret) < 32 {
		return nil, fmt.Errorf("billing adapter secret must contain at least 32 bytes")
	}
	return &HTTP{base: strings.TrimRight(u.String(), "/"), secret: []byte(c.Secret), client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

type HTTP struct {
	base   string
	secret []byte
	client *http.Client
}

func (*HTTP) Enabled() bool { return true }

// Signature binds time, method, full path and exact JSON bytes. Keys belong only
// to servers. Requests may be replayed inside the time window, so the receiver
// MUST persist idempotency keys and reject reuse with a different payload.
func Signature(secret []byte, timestamp, method, path string, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	fmt.Fprintf(mac, "%s\n%s\n%s\n", timestamp, method, path)
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
func (a *HTTP) Do(ctx context.Context, action string, input Request) (Decision, error) {
	if err := validate(action, input); err != nil {
		return Decision{}, err
	}
	body, err := json.Marshal(input)
	if err != nil {
		return Decision{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.base+"/v1/"+action, bytes.NewReader(body))
	if err != nil {
		return Decision{}, ErrInvalid
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Misty-Billing-Timestamp", timestamp)
	req.Header.Set("X-Misty-Billing-Signature", Signature(a.secret, timestamp, req.Method, req.URL.EscapedPath(), body))
	res, err := a.client.Do(req)
	if err != nil {
		return Decision{}, fmt.Errorf("%w: transport failure", ErrUnavailable)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusPaymentRequired || res.StatusCode == http.StatusForbidden {
		return Decision{}, ErrDenied
	}
	if res.StatusCode == http.StatusBadRequest {
		return Decision{}, ErrInvalid
	}
	if res.StatusCode == http.StatusConflict {
		return Decision{}, ErrConflict
	}
	if res.StatusCode != http.StatusOK {
		return Decision{}, fmt.Errorf("%w: HTTP %d", ErrUnavailable, res.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return Decision{}, ErrUnavailable
	}
	var result Decision
	if json.Unmarshal(raw, &result) != nil {
		return Decision{}, ErrUnavailable
	}
	if !result.Allowed {
		return Decision{}, ErrDenied
	}
	if action == "reserve" && strings.TrimSpace(result.ReservationID) == "" {
		return Decision{}, ErrUnavailable
	}
	return result, nil
}

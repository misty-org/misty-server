package console

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// maxOperatorAnswer has room for an account export, the largest answer there is.
const maxOperatorAnswer = 64 << 20

// operatorClient changes accounts through the running server's operator
// account API, so the console never re-implements limits, suspension or
// setup keys, and the server applies a suspension the moment it is made.
type operatorClient struct {
	base   string
	token  string
	client *http.Client
}

var errOperatorUnconfigured = errors.New("Set MISTY_OPERATOR_TOKEN for the console (the same value the server reads) to change accounts here.")

func newOperatorClient(apiURL, token string, client *http.Client) *operatorClient {
	return &operatorClient{base: strings.TrimRight(apiURL, "/") + "/operator/v1", token: strings.TrimSpace(token), client: client}
}

func (c *operatorClient) configured() bool { return c != nil && c.token != "" }

func (c *operatorClient) setLimits(ctx context.Context, id string, limits map[string]any) error {
	return c.do(ctx, http.MethodPatch, "/accounts/"+url.PathEscape(id), map[string]any{"limits": limits})
}

func (c *operatorClient) suspend(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/accounts/"+url.PathEscape(id)+"/suspend", nil)
}

func (c *operatorClient) resume(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/accounts/"+url.PathEscape(id)+"/resume", nil)
}

// close makes the account read-only and deletes it in 30 days unless resumed.
func (c *operatorClient) close(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/accounts/"+url.PathEscape(id)+"/close", map[string]any{"delete_after_days": 30})
}

// signInLink issues a new setup key and returns its sign-in link. The console
// shows the link to pass on, since a server without Mailjet configured sends
// no email; earlier unused links stop working.
func (c *operatorClient) signInLink(ctx context.Context, id string) (string, error) {
	var answer struct {
		Account struct {
			SetupLink string `json:"setup_link"`
		} `json:"account"`
	}
	if err := c.call(ctx, http.MethodPost, "/accounts/"+url.PathEscape(id)+"/setup-key", nil, &answer); err != nil {
		return "", err
	}
	if answer.Account.SetupLink == "" {
		return "", errors.New("the server answered without a sign-in link")
	}
	return answer.Account.SetupLink, nil
}

// exportAccount asks for the account's export. Signing a link for every file
// can take a while, so it waits longer than the other calls.
func (c *operatorClient) exportAccount(ctx context.Context, id string) (AccountExportManifest, error) {
	patient := *c
	patient.client = &http.Client{Timeout: 2 * time.Minute}
	var manifest AccountExportManifest
	err := patient.call(ctx, http.MethodPost, "/accounts/"+url.PathEscape(id)+"/export", nil, &manifest)
	return manifest, err
}

func (c *operatorClient) do(ctx context.Context, method, path string, body any) error {
	return c.call(ctx, method, path, body, nil)
}

// call sends one request and, when out is set, decodes a successful answer
// into it.
func (c *operatorClient) call(ctx context.Context, method, path string, body any, out any) error {
	if !c.configured() {
		return errOperatorUnconfigured
	}
	var reader io.Reader = http.NoBody
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("the server's operator API is unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 300 {
		if out == nil {
			return nil
		}
		return json.NewDecoder(io.LimitReader(resp.Body, maxOperatorAnswer)).Decode(out)
	}
	var problem struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&problem)
	switch {
	case resp.StatusCode == http.StatusNotFound && problem.Code == "":
		return errors.New("The server has no operator API. Set MISTY_OPERATOR_TOKEN on the server and restart it.")
	case problem.Code == "operator_unauthorized":
		return errors.New("The server refused the console's MISTY_OPERATOR_TOKEN.")
	case problem.Message != "":
		return errors.New(problem.Message)
	case problem.Code != "":
		return errors.New(humanize(problem.Code))
	}
	return fmt.Errorf("the server answered %s", resp.Status)
}

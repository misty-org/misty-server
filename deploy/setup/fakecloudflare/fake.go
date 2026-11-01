// Package fakecloudflare is a stand-in for the parts of the Cloudflare API the
// setup container uses (tunnels, DNS, R2 buckets and tokens, Workers), plus a
// minimal S3 endpoint for backups. Tests and the stack test run against it so
// nothing ever calls the real API.
package fakecloudflare

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// Script is one uploaded Worker.
type Script struct {
	Metadata     map[string]any
	Modules      map[string][]byte
	MigrationTag string
	Subdomain    bool
	Uploads      int
}

// Bucket is one R2 bucket and its rules.
type Bucket struct {
	CORS, Lock, Lifecycle any
	Objects               map[string][]byte
}

// Token is an account API token.
type Token struct {
	ID, Name, Value string
	Policies        any
}

// Fake holds the fake account's state. Lock Mu to read it while serving.
type Fake struct {
	Mu        sync.Mutex
	APIToken  string
	AccountID string
	ZoneID    string
	ZoneName  string
	Subdomain string

	Tunnels  map[string]map[string]any // id -> tunnel
	Configs  map[string]any            // tunnel id -> configuration
	DNS      map[string]map[string]any // id -> record
	Buckets  map[string]*Bucket
	Tokens   map[string]*Token
	Scripts  map[string]*Script
	Requests []string
	// Fail makes every API call answer 503, as during an outage.
	Fail bool

	next int
}

func New(apiToken, accountID, zoneID, zoneName string) *Fake {
	return &Fake{
		APIToken: apiToken, AccountID: accountID, ZoneID: zoneID, ZoneName: zoneName, Subdomain: "misty-test",
		Tunnels: map[string]map[string]any{}, Configs: map[string]any{}, DNS: map[string]map[string]any{},
		Buckets: map[string]*Bucket{}, Tokens: map[string]*Token{}, Scripts: map[string]*Script{},
	}
}

func (f *Fake) id() string {
	f.next++
	return fmt.Sprintf("%032x", f.next)
}

// Count returns how many requests matched "METHOD path-prefix".
func (f *Fake) Count(prefix string) int {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	count := 0
	for _, request := range f.Requests {
		if strings.HasPrefix(request, prefix) {
			count++
		}
	}
	return count
}

type envelope struct {
	Success  bool             `json:"success"`
	Errors   []map[string]any `json:"errors"`
	Messages []any            `json:"messages"`
	Result   any              `json:"result"`
}

func reply(w http.ResponseWriter, status int, result any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body := envelope{Success: status < 300, Errors: []map[string]any{}, Messages: []any{}, Result: result}
	if status >= 300 {
		body.Errors = []map[string]any{{"code": status * 10, "message": http.StatusText(status)}}
		body.Result = nil
	}
	_ = json.NewEncoder(w).Encode(body)
}

func decode(r *http.Request) map[string]any {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body == nil {
		body = map[string]any{}
	}
	return body
}

func (f *Fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256") {
		f.serveS3(w, r)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/client/v4")
	f.Requests = append(f.Requests, r.Method+" "+path)
	if f.Fail {
		reply(w, http.StatusServiceUnavailable, nil)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+f.APIToken {
		reply(w, http.StatusForbidden, nil)
		return
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	switch {
	case len(parts) >= 2 && parts[0] == "zones" && parts[1] == f.ZoneID:
		f.serveZone(w, r, parts[2:])
	case len(parts) >= 2 && parts[0] == "accounts" && parts[1] == f.AccountID:
		f.serveAccount(w, r, parts[2:])
	default:
		reply(w, http.StatusNotFound, nil)
	}
}

func (f *Fake) serveZone(w http.ResponseWriter, r *http.Request, parts []string) {
	switch {
	case len(parts) == 0 && r.Method == "GET":
		reply(w, 200, map[string]any{"id": f.ZoneID, "name": f.ZoneName, "account": map[string]any{"id": f.AccountID}})
	case len(parts) == 1 && parts[0] == "dns_records" && r.Method == "GET":
		records := []map[string]any{}
		for _, record := range f.DNS {
			if record["name"] == r.URL.Query().Get("name") {
				records = append(records, record)
			}
		}
		reply(w, 200, records)
	case len(parts) == 1 && parts[0] == "dns_records" && r.Method == "POST":
		record := decode(r)
		record["id"] = f.id()
		f.DNS[record["id"].(string)] = record
		reply(w, 200, record)
	case len(parts) == 2 && parts[0] == "dns_records" && r.Method == "PATCH" && f.DNS[parts[1]] != nil:
		for key, value := range decode(r) {
			f.DNS[parts[1]][key] = value
		}
		reply(w, 200, f.DNS[parts[1]])
	default:
		reply(w, http.StatusNotFound, nil)
	}
}

func (f *Fake) serveAccount(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) == 0 {
		reply(w, http.StatusNotFound, nil)
		return
	}
	switch parts[0] {
	case "cfd_tunnel":
		f.serveTunnels(w, r, parts[1:])
	case "r2":
		f.serveR2(w, r, parts[1:])
	case "tokens":
		f.serveTokens(w, r, parts[1:])
	case "workers":
		f.serveWorkers(w, r, parts[1:])
	default:
		reply(w, http.StatusNotFound, nil)
	}
}

func (f *Fake) serveTunnels(w http.ResponseWriter, r *http.Request, parts []string) {
	switch {
	case len(parts) == 0 && r.Method == "GET":
		tunnels := []map[string]any{}
		for _, tunnel := range f.Tunnels {
			if name := r.URL.Query().Get("name"); name == "" || tunnel["name"] == name {
				tunnels = append(tunnels, tunnel)
			}
		}
		reply(w, 200, tunnels)
	case len(parts) == 0 && r.Method == "POST":
		body := decode(r)
		tunnel := map[string]any{"id": f.id(), "name": body["name"], "config_src": body["config_src"]}
		f.Tunnels[tunnel["id"].(string)] = tunnel
		reply(w, 200, tunnel)
	case len(parts) == 2 && f.Tunnels[parts[0]] != nil && parts[1] == "configurations" && r.Method == "PUT":
		f.Configs[parts[0]] = decode(r)["config"]
		reply(w, 200, map[string]any{"tunnel_id": parts[0]})
	case len(parts) == 2 && f.Tunnels[parts[0]] != nil && parts[1] == "token" && r.Method == "GET":
		reply(w, 200, "fake-tunnel-token-"+parts[0])
	default:
		reply(w, http.StatusNotFound, nil)
	}
}

func (f *Fake) serveR2(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) == 0 || parts[0] != "buckets" {
		reply(w, http.StatusNotFound, nil)
		return
	}
	parts = parts[1:]
	switch {
	case len(parts) == 0 && r.Method == "POST":
		name, _ := decode(r)["name"].(string)
		if f.Buckets[name] != nil {
			reply(w, http.StatusConflict, nil)
			return
		}
		f.Buckets[name] = &Bucket{Objects: map[string][]byte{}}
		reply(w, 200, map[string]any{"name": name})
	case len(parts) >= 1 && f.Buckets[parts[0]] == nil:
		reply(w, http.StatusNotFound, nil)
	case len(parts) == 1 && r.Method == "GET":
		reply(w, 200, map[string]any{"name": parts[0]})
	case len(parts) == 2 && r.Method == "PUT":
		bucket, body := f.Buckets[parts[0]], decode(r)
		switch parts[1] {
		case "cors":
			bucket.CORS = body
		case "lock":
			bucket.Lock = body
		case "lifecycle":
			bucket.Lifecycle = body
		default:
			reply(w, http.StatusNotFound, nil)
			return
		}
		reply(w, 200, map[string]any{})
	default:
		reply(w, http.StatusNotFound, nil)
	}
}

func (f *Fake) serveTokens(w http.ResponseWriter, r *http.Request, parts []string) {
	switch {
	case len(parts) == 1 && parts[0] == "permission_groups" && r.Method == "GET":
		reply(w, 200, []map[string]any{
			{"id": "perm-r2-read", "name": "Workers R2 Storage Bucket Item Read"},
			{"id": "perm-r2-write", "name": "Workers R2 Storage Bucket Item Write"},
		})
	case len(parts) == 0 && r.Method == "GET":
		tokens := []map[string]any{}
		for _, token := range f.Tokens {
			tokens = append(tokens, map[string]any{"id": token.ID, "name": token.Name})
		}
		reply(w, 200, tokens)
	case len(parts) == 0 && r.Method == "POST":
		body := decode(r)
		token := &Token{ID: f.id(), Name: fmt.Sprint(body["name"]), Value: "value-" + f.id(), Policies: body["policies"]}
		f.Tokens[token.ID] = token
		reply(w, 200, map[string]any{"id": token.ID, "name": token.Name, "value": token.Value})
	case len(parts) >= 1 && f.Tokens[parts[0]] == nil:
		reply(w, http.StatusNotFound, nil)
	case len(parts) == 1 && r.Method == "GET":
		reply(w, 200, map[string]any{"id": parts[0], "name": f.Tokens[parts[0]].Name})
	case len(parts) == 2 && parts[1] == "value" && r.Method == "PUT":
		f.Tokens[parts[0]].Value = "rolled-" + f.id()
		reply(w, 200, f.Tokens[parts[0]].Value)
	default:
		reply(w, http.StatusNotFound, nil)
	}
}

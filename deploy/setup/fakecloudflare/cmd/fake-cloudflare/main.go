// Command fake-cloudflare serves the fake Cloudflare API for the stack test
// (server/deploy/setup/stack-test.sh). GET /_state dumps what it holds.
package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"

	"github.com/kannachi323/misty/server/deploy/setup/fakecloudflare"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:0", "listen address")
	token := flag.String("token", "fake-cloudflare-token", "API token the fake accepts")
	account := flag.String("account", "0123456789abcdef0123456789abcdef", "account ID")
	zone := flag.String("zone", "fedcba9876543210fedcba9876543210", "zone ID")
	zoneName := flag.String("zone-name", "misty.example", "zone name")
	tlsAddr := flag.String("tls-addr", "", "also serve over HTTPS here (the S3 stand-in, which must be HTTPS)")
	tlsCert := flag.String("tls-cert", "", "certificate for -tls-addr")
	tlsKey := flag.String("tls-key", "", "key for -tls-addr")
	flag.Parse()
	fake := fakecloudflare.New(*token, *account, *zone, *zoneName)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /_state", func(w http.ResponseWriter, _ *http.Request) {
		fake.Mu.Lock()
		defer fake.Mu.Unlock()
		scripts := map[string]any{}
		for name, script := range fake.Scripts {
			scripts[name] = map[string]any{"uploads": script.Uploads, "migration_tag": script.MigrationTag, "subdomain": script.Subdomain}
		}
		buckets := map[string]any{}
		for name, bucket := range fake.Buckets {
			buckets[name] = map[string]any{"objects": len(bucket.Objects), "lock": bucket.Lock != nil}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tunnels": len(fake.Tunnels), "dns": len(fake.DNS), "tokens": len(fake.Tokens),
			"scripts": scripts, "buckets": buckets, "requests": len(fake.Requests),
		})
	})
	mux.Handle("/", fake)
	if *tlsAddr != "" {
		go func() { log.Fatal(http.ListenAndServeTLS(*tlsAddr, *tlsCert, *tlsKey, mux)) }()
	}
	log.Printf("fake Cloudflare listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

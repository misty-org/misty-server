package app

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
)

const activepiecesProxyPrefix = "/activepieces"

func activepiecesProxyFromEnv() (http.Handler, error) {
	raw := strings.TrimSpace(envconfig.Getenv("MISTY_ACTIVEPIECES_PROXY_URL"))
	if raw == "" {
		return nil, nil
	}
	return newActivepiecesProxy(raw)
}

func newActivepiecesProxy(raw string) (http.Handler, error) {
	target, err := url.Parse(raw)
	if err != nil || target.Scheme != "http" || target.Host == "" || target.User != nil || target.RawQuery != "" || target.Fragment != "" {
		return nil, errors.New("MISTY_ACTIVEPIECES_PROXY_URL must be a complete internal HTTP origin")
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	defaultDirector := proxy.Director
	proxy.Director = func(request *http.Request) {
		defaultDirector(request)
		request.Header.Del("Accept-Encoding")
		request.URL.Path = activepiecesUpstreamPath(request.URL.Path)
		request.URL.RawPath = ""
	}
	proxy.ModifyResponse = rewriteActivepiecesHTML
	return proxy, nil
}

func activepiecesUpstreamPath(path string) string {
	switch path {
	case "/.well-known/oauth-authorization-server/activepieces":
		return "/.well-known/oauth-authorization-server"
	case "/.well-known/openid-configuration/activepieces":
		return "/.well-known/openid-configuration"
	}
	if strings.HasPrefix(path, "/.well-known/oauth-protected-resource/activepieces/") {
		return "/.well-known/oauth-protected-resource/" + strings.TrimPrefix(path, "/.well-known/oauth-protected-resource/activepieces/")
	}
	if path == activepiecesProxyPrefix {
		return "/"
	}
	if strings.HasPrefix(path, activepiecesProxyPrefix+"/") {
		return strings.TrimPrefix(path, activepiecesProxyPrefix)
	}
	return path
}

func rewriteActivepiecesHTML(response *http.Response) error {
	if !strings.HasPrefix(response.Header.Get("Content-Type"), "text/html") {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return err
	}
	_ = response.Body.Close()
	body = bytes.ReplaceAll(body, []byte(`href="/`), []byte(`href="/activepieces/`))
	body = bytes.ReplaceAll(body, []byte(`src="/`), []byte(`src="/activepieces/`))
	response.Body = io.NopCloser(bytes.NewReader(body))
	response.ContentLength = int64(len(body))
	response.Header.Set("Content-Length", strconv.Itoa(len(body)))
	return nil
}

func mountActivepiecesProxy(router chi.Router, proxy http.Handler) {
	// The pinned Activepieces release advertises AP_FRONTEND_URL as its public
	// origin, but serves its UI, API, and MCP transport from upstream root paths.
	// The proxy strips Misty's namespace, rebases UI assets, and forwards the
	// resource-specific RFC 8414/9728 routes without taking over Misty's /mcp.
	router.Handle(activepiecesProxyPrefix, proxy)
	router.Handle(activepiecesProxyPrefix+"/*", proxy)
	router.Handle("/.well-known/oauth-protected-resource/activepieces/*", proxy)
	router.Handle("/.well-known/oauth-authorization-server/activepieces", proxy)
	router.Handle("/.well-known/openid-configuration/activepieces", proxy)
}

func TestingNewActivepiecesProxy(raw string) (http.Handler, error) {
	return newActivepiecesProxy(raw)
}

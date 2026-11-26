package app

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/misty-org/misty-server/internal/platform/config"
)

const (
	roomsPath       = "/v1/collab"
	callsPath       = "/v1/calls"
	callsSignalPath = callsPath + "/rtc"
)

func underPath(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

func isLongLivedRequest(r *http.Request) bool {
	return IsWebSocketRequest(r) || underPath(r.URL.Path, roomsPath) || underPath(r.URL.Path, callsSignalPath)
}

func edgeHandler(api, rooms, liveKit http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case rooms != nil && underPath(r.URL.Path, roomsPath):
			rooms.ServeHTTP(w, r)
		case liveKit != nil && underPath(r.URL.Path, callsSignalPath):
			liveKit.ServeHTTP(w, r)
		default:
			api.ServeHTTP(w, r)
		}
	})
}

func liveKitProxyFromEnv() (http.Handler, error) {
	public := strings.TrimSpace(config.Getenv("LIVEKIT_URL"))
	admin := strings.TrimSpace(config.Getenv("LIVEKIT_ADMIN_URL"))
	if public == "" || admin == "" {
		return nil, nil
	}
	target, err := url.Parse(admin)
	if err != nil || target.Scheme == "" || target.Host == "" {
		return nil, fmt.Errorf("LIVEKIT_ADMIN_URL must be an absolute URL: %q", admin)
	}
	return liveKitProxy(target), nil
}

func liveKitProxy(target *url.URL) http.Handler {
	return &httputil.ReverseProxy{
		Rewrite: func(out *httputil.ProxyRequest) {
			out.SetURL(target)
			path := strings.TrimPrefix(out.In.URL.Path, callsPath)
			if path == "" {
				path = "/"
			}
			out.Out.URL.Path = strings.TrimRight(target.Path, "/") + path
			out.Out.URL.RawPath = ""
			out.SetXForwarded()
		},
	}
}

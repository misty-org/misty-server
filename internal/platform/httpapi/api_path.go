package api

import "strings"

func unversionedAPIPath(path string) string {
	path = "/" + strings.TrimLeft(strings.TrimSpace(path), "/")
	if strings.HasPrefix(path, "/api/") {
		return strings.TrimPrefix(path, "/api")
	}
	if strings.HasPrefix(path, "/v1/") {
		return strings.TrimPrefix(path, "/v1")
	}
	return path
}

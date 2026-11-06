package api

import (
	"net/http"
	"testing"
)

func TestOperatorExportNeedsTheTokenAndNoPassword(t *testing.T) {
	handler := readOnlyServer(t)
	session := signIn(t, handler)
	_, me := call(t, handler, http.MethodGet, "/v1/me", "", "", session...)
	id, _ := me["id"].(string)
	path := "/operator/v1/accounts/" + id + "/export"

	if rec, body := call(t, handler, http.MethodPost, path, "wrong-token", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token = %d %v", rec.Code, body)
	}
	// A person's own session is not enough; this is the operator's route.
	if rec, body := call(t, handler, http.MethodPost, path, "", "", session...); rec.Code != http.StatusUnauthorized {
		t.Fatalf("session = %d %v", rec.Code, body)
	}
	if rec, body := call(t, handler, http.MethodPost, "/operator/v1/accounts/no-such-account/export", operatorToken, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown account = %d %v", rec.Code, body)
	}
	rec, body := call(t, handler, http.MethodPost, path, operatorToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("export = %d %v", rec.Code, body)
	}
	data, _ := body["account_data"].(map[string]any)
	account, _ := data["account"].(map[string]any)
	if account["email"] != "reader@example.com" {
		t.Fatalf("export should hold the account's data: %v", body)
	}
	if _, ok := body["documents"]; !ok {
		t.Fatalf("export should list documents: %v", body)
	}
	if cache := rec.Header().Get("Cache-Control"); cache != "no-store" {
		t.Fatalf("Cache-Control = %q", cache)
	}
}

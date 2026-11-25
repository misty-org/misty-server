package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAppVersionsOrderAsSemver(t *testing.T) {
	ordered := []string{
		"0.1.0-alpha",
		"0.1.0-alpha.1",
		"0.1.0-alpha.beta",
		"0.1.0-beta",
		"0.1.0-beta.2",
		"0.1.0-beta.11",
		"0.1.0-rc.1",
		"0.1.0",
		"0.1.1",
		"0.2.0",
		"1.0.0",
	}
	for index := 1; index < len(ordered); index++ {
		lower, ok := parseAppVersion(ordered[index-1])
		if !ok {
			t.Fatalf("%s did not parse", ordered[index-1])
		}
		higher, ok := parseAppVersion(ordered[index])
		if !ok {
			t.Fatalf("%s did not parse", ordered[index])
		}
		if compareAppVersions(lower, higher) >= 0 || compareAppVersions(higher, lower) <= 0 {
			t.Errorf("%s should come before %s", ordered[index-1], ordered[index])
		}
	}
	same, _ := parseAppVersion("v0.1.0-beta.1+build.7")
	other, _ := parseAppVersion("0.1.0-beta.1")
	if compareAppVersions(same, other) != 0 {
		t.Error("a leading v and build metadata should not change the order")
	}
}

func TestMalformedAppVersionsAreRejected(t *testing.T) {
	for _, raw := range []string{"", "1", "1.2", "1.2.3.4", "1.2.x", "1.2.3-", "1.2.3-beta..1", "-1.2.3"} {
		if _, ok := parseAppVersion(raw); ok {
			t.Errorf("%q should not parse", raw)
		}
	}
}

func TestAppsBelowTheMinimumAreAskedToUpdate(t *testing.T) {
	minimum, _ := parseAppVersion("0.1.0-beta.2")
	handler := requireAppVersion(minimum, "0.1.0-beta.2")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	cases := map[string]int{
		"":             http.StatusNoContent,
		"not-a-number": http.StatusNoContent,
		"0.1.0-beta.1": http.StatusUpgradeRequired,
		"0.1.0-beta.2": http.StatusNoContent,
		"0.1.0":        http.StatusNoContent,
	}
	for version, want := range cases {
		request := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
		if version != "" {
			request.Header.Set(appVersionHeader, version)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != want {
			t.Errorf("version %q: got %d, want %d", version, recorder.Code, want)
		}
		if want != http.StatusUpgradeRequired {
			continue
		}
		var body map[string]string
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body["code"] != "app_update_required" || body["minimum"] != "0.1.0-beta.2" {
			t.Errorf("unexpected body %v", body)
		}
	}
}

package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadonlyResponseMasksCredentialsAndPreservesPanelShape(t *testing.T) {
	body := `{"api-keys":["secret-one","secret-two"],"providers":[{"api-key":"secret-one","alias":"work","models":["model-a"],"headers":{"Authorization":"secret-three"}}],"cpaConnection":{"managementKey":"secret-four","managementKeyConfigured":true,"cpaBaseUrl":"https://u:secret-five@example.test?api_key=secret-six"},"summary":{"total_tokens":9007199254740993,"total_cost":12.75},"events":{"items":[{"raw_json":"secret-seven","fail_body":"secret-eight","model":"model-a","input_tokens":23}]},"refresh_token":{"value":"secret-nine"},"logs":["secret-ten"]}`
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Set-Cookie", "secret-cookie")
		w.Header().Set("X-Cpa-Version", "v1")
		_, _ = w.Write([]byte(body))
	})
	w := httptest.NewRecorder()
	serveViewerRead(w, httptest.NewRequest("GET", "/v0/management/config", nil), next)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "secret-") {
		t.Fatal("credential escaped redaction")
	}
	if !strings.Contains(w.Body.String(), "9007199254740993") || !strings.Contains(w.Body.String(), `"model":"model-a"`) {
		t.Fatal("panel metrics/shape lost")
	}
	if w.Header().Get("Set-Cookie") != "" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("unsafe response headers")
	}
	var out struct {
		Keys []string `json:"api-keys"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Keys) != 2 || out.Keys[0] == out.Keys[1] || out.Keys[0] != maskViewerKey("secret-one") {
		t.Fatal("masked credential identifiers must stay distinct and stable")
	}
}
func TestReadonlyResponseFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		body   string
		status int
	}{{"secret raw text", 200}, {`"secret encoded text"`, 200}, {`{} "secret trailing text"`, 200}, {`{"error":"secret backend error"}`, 500}} {
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		})
		w := httptest.NewRecorder()
		serveViewerRead(w, httptest.NewRequest("GET", "/v0/management/config", nil), next)
		if w.Code < 400 || strings.Contains(w.Body.String(), "secret") {
			t.Fatal(w.Code, w.Body)
		}
	}
	w := &viewerWriter{header: make(http.Header)}
	w.body.Write(make([]byte, viewerResponseLimit))
	if _, err := w.Write([]byte("x")); err == nil || !w.overflow {
		t.Fatal("unbounded response")
	}
}
func TestReadonlyRoutesSeparateQueriesFromMutations(t *testing.T) {
	for _, path := range []string{"/v0/management/config", "/v0/management/auth-files", "/v0/management/dashboard/summary", "/usage-service/config"} {
		if !ViewerRouteAllowed("GET", path) {
			t.Fatal(path)
		}
		for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
			if ViewerRouteAllowed(method, path) {
				t.Fatal(method, path)
			}
		}
	}
	for _, path := range []string{"/v0/management/monitoring/analytics", "/v0/management/quota-snapshots/query", "/v0/management/monitoring/account-history"} {
		if !ViewerRouteAllowed("POST", path) {
			t.Fatal(path)
		}
	}
	for _, path := range []string{"/v0/management/codex-auth-url", "/v0/management/auth-files/download", "/v0/management/api-call", "/v0/management/future", "/v0/resource/plugins/test/secret"} {
		if ViewerRouteAllowed("GET", path) || ViewerRouteAllowed("POST", path) {
			t.Fatal(path)
		}
	}
}

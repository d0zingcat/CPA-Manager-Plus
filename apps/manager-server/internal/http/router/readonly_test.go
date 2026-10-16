package router

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/app"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/middleware"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/adminauth"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/apikeyalias"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/monitoring"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usage"
)

func TestReadonlyAccessLifecycleAndRouteBoundary(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "usage.sqlite")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	credential, err := security.NewAdminCredential("cpamp_admin_test", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err = st.SaveAdminCredential(ctx, credential); err != nil {
		t.Fatal(err)
	}
	auth := adminauth.New(config.Config{}, st)
	appCtx := &app.Context{Store: st, AdminAuthService: auth, APIKeyAliasService: apikeyalias.New(st), MonitoringService: monitoring.New(st)}
	handler := New(appCtx)
	request := func(method, path, key, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	status := request("GET", "/usage-service/readonly-key", "cpamp_admin_test", "")
	if status.Code != 200 || !strings.Contains(status.Body.String(), `"enabled":false`) {
		t.Fatalf("default: %d %s", status.Code, status.Body)
	}
	rotate := func() string {
		t.Helper()
		w := request("POST", "/usage-service/readonly-key", "cpamp_admin_test", "")
		if w.Code != 200 {
			t.Fatal(w.Body)
		}
		var v struct {
			Key string `json:"key"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		return v.Key
	}
	key := rotate()
	if !strings.HasPrefix(key, "cpamp_view_") {
		t.Fatal("viewer prefix missing")
	}
	if ok, err := auth.VerifyHeader(ctx, "Bearer "+key); err != nil || ok {
		t.Fatal("viewer accepted as admin")
	}
	if ok, err := auth.VerifyPanelHeader(ctx, "Bearer "+key); err != nil || ok {
		t.Fatal("viewer accepted as panel admin")
	}
	w := request("GET", "/usage-service/readonly-key", "cpamp_admin_test", "")
	if strings.Contains(w.Body.String(), key) || strings.Contains(w.Body.String(), "keyHash") || strings.Contains(w.Body.String(), "salt") {
		t.Fatal("credential exposed in status")
	}
	for _, method := range []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE"} {
		for _, path := range []string{
			"/status", "/usage-service/info", "/usage-service/config", "/usage-service/updates", "/usage-service/account-processing-policy", "/usage-service/quota-cooldowns", "/setup", "/usage-service/readonly-key",
			"/v0/management/config", "/v0/management/config.yaml", "/v0/management/api-keys", "/v0/management/auth-files", "/v0/management/auth-files/download", "/v0/management/logs", "/v0/management/api-call",
			"/v0/management/usage", "/v0/management/usage/export", "/v0/management/usage/import", "/v0/management/usage/import-sessions", "/v0/management/usage/archives", "/v0/management/usage/maintenance",
			"/v0/management/model-prices", "/v0/management/api-key-aliases", "/v0/management/account-action-candidates", "/v0/management/codex-inspection", "/v0/management/dashboard/summary",
			"/v0/management/monitoring/header-snapshots", "/v0/management/monitoring/account-history", "/v0/management/monitoring/account-window-usage", "/v0/management/quota-snapshots", "/v0/management/quota-snapshots/query",
			"/v0/management/cpa-connection/validate", "/v0/management/plugins", "/v0/resource/plugins/test/secret", "/models", "/v1/models", "/future-endpoint", "/usage-service/viewer/metadata",
			"/usage-service/auth/session/", "/v0/management/monitoring/analytics/", "/v0//management/monitoring/analytics",
		} {
			if middleware.ViewerRouteAllowed(method, path) {
				continue
			}
			if got := request(method, path, key, ""); got.Code != 403 {
				t.Fatalf("%s %s = %d %s", method, path, got.Code, got.Body)
			}
		}
	}
	for _, path := range []string{"/usage-service/auth/session"} {
		if got := request("GET", path, key, ""); got.Code != 200 {
			t.Fatalf("%s = %d %s", path, got.Code, got.Body)
		}
	}
	if err := st.SaveModelPrices(ctx, map[string]store.ModelPrice{"test-model": {Prompt: 1, Completion: 2, PromptConfigured: true, CompletionConfigured: true}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertEvents(ctx, []usage.Event{
		{EventHash: strings.Repeat("b", 64), TimestampMS: 1000000000000, Model: "test-model", Provider: "codex", AuthProviderSnapshot: "codex", APIKeyHash: strings.Repeat("a", 64), InputTokens: 100, OutputTokens: 20, TotalTokens: 120, Endpoint: "sensitive-canary", Source: "sensitive-canary", FailSummary: "sensitive-canary"},
		{EventHash: strings.Repeat("c", 64), TimestampMS: 1000000000001, Model: "test-model", Provider: "codex", AuthProviderSnapshot: "codex", APIKeyHash: strings.Repeat("a", 64), InputTokens: 50, OutputTokens: 10, TotalTokens: 60, Failed: true, FailStatusCode: 429, FailSummary: "sensitive-canary"},
	}); err != nil {
		t.Fatal(err)
	}
	query := `{"from_ms":1,"to_ms":2000000000000,"include":{"summary":true,"model_stats":true,"events_page":{"limit":50}}}`
	viewerResult := request("POST", "/v0/management/monitoring/analytics", key, query)
	adminResult := request("POST", "/v0/management/monitoring/analytics", "cpamp_admin_test", query)
	if viewerResult.Code != 200 || adminResult.Code != 200 {
		t.Fatalf("analytics: viewer=%d %s admin=%d %s", viewerResult.Code, viewerResult.Body, adminResult.Code, adminResult.Body)
	}
	if strings.Contains(viewerResult.Body.String(), "sensitive-canary") {
		t.Fatal("sensitive event leaked")
	}
	var viewerSummary, adminSummary struct {
		Summary struct {
			Calls  int64   `json:"total_calls"`
			Tokens int64   `json:"total_tokens"`
			Cost   float64 `json:"total_cost"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(viewerResult.Body.Bytes(), &viewerSummary); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(adminResult.Body.Bytes(), &adminSummary); err != nil {
		t.Fatal(err)
	}
	if viewerSummary != adminSummary || viewerSummary.Summary.Calls != 2 || viewerSummary.Summary.Tokens != 180 || viewerSummary.Summary.Cost <= 0 {
		t.Fatalf("viewer/admin metrics differ: viewer=%+v admin=%+v", viewerSummary, adminSummary)
	}
	for _, test := range []struct {
		Filters string
		Calls   int64
	}{
		{`{"models":["missing-model"]}`, 0}, {`{"providers":["codex"]}`, 2}, {`{"api_key_hashes":["` + strings.Repeat("a", 64) + `"]}`, 2}, {`{"failed_only":true}`, 1},
	} {
		body := `{"from_ms":1,"to_ms":2000000000000,"filters":` + test.Filters + `,"include":{"summary":true}}`
		got := request("POST", "/v0/management/monitoring/analytics", key, body)
		if got.Code != 200 {
			t.Fatalf("filter: %d %s", got.Code, got.Body)
		}
		if err := json.Unmarshal(got.Body.Bytes(), &viewerSummary); err != nil {
			t.Fatal(err)
		}
		if viewerSummary.Summary.Calls != test.Calls {
			t.Fatalf("filter %s calls=%d want=%d", test.Filters, viewerSummary.Summary.Calls, test.Calls)
		}
	}
	for _, query := range []string{
		`{"from_ms":1,"to_ms":2,"include":{"events_page":{"limit":50001}}}`,
	} {
		if got := request("POST", "/v0/management/monitoring/analytics", key, query); got.Code != 400 {
			t.Fatalf("invalid query = %d %s", got.Code, got.Body)
		}
	}

	if got := request("POST", "/v0/management/monitoring/analytics", key, `{"search_query":"`+strings.Repeat("x", 1024*1024)+`"}`); got.Code != 400 {
		t.Fatalf("oversized query = %d", got.Code)
	}
	if got := request("GET", "/usage-service/viewer/metadata", "cpamp_admin_test", ""); got.Code != 404 {
		t.Fatalf("retired metadata endpoint = %d", got.Code)
	}
	// Reopen storage: no bootstrap or process-local authentication state is required.
	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	role, err := adminauth.New(config.Config{}, reopened).Role(ctx, "Bearer "+key)
	if err != nil || role != adminauth.RoleUsageViewer {
		t.Fatalf("persisted role: %q %v", role, err)
	}
	old := key
	key = rotate()
	if got := request("GET", "/usage-service/auth/session", old, ""); got.Code != 401 {
		t.Fatal("old key survived rotation")
	}
	if got := request("GET", "/usage-service/auth/session", key, ""); got.Code != 200 {
		t.Fatal("new key rejected")
	}
	if got := request("DELETE", "/usage-service/readonly-key", "cpamp_admin_test", ""); got.Code != 200 {
		t.Fatal("disable failed")
	}
	if got := request("GET", "/usage-service/auth/session", key, ""); got.Code != 401 {
		t.Fatal("disabled key accepted")
	}
	if got := request("GET", "/usage-service/auth/session", "cpamp_admin_test", ""); got.Code != http.StatusOK {
		t.Fatal("admin changed")
	}
}

func TestReadonlySharedPanelReadsAreRedactedAndWritesNeverReachCPA(t *testing.T) {
	var writes int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			writes++
			w.WriteHeader(500)
			return
		}
		if r.Header.Get("Authorization") != "Bearer upstream-test-key" {
			t.Error("saved upstream key missing")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"api-keys":["secret-canary"],"request-retry":3,"providers":[{"api-key":"secret-canary","models":["model-a"]}]}`))
	}))
	defer upstream.Close()
	st, err := store.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	credential, _ := security.NewAdminCredential("admin-test-key", "test")
	if err = st.SaveAdminCredential(context.Background(), credential); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{CPAUpstreamURL: upstream.URL, ManagementKey: "upstream-test-key"}
	appCtx := app.FromExisting(cfg, st, nil, 0, nil, nil, nil, "test")
	key, err := appCtx.AdminAuthService.RotateReadonlyKey(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	handler := New(appCtx)
	request := func(method, path, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(`{}`))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, path := range []string{"/v0/management/config", "/v0/management/config.yaml", "/v0/management/auth-files"} {
		w := request("GET", path, key)
		if w.Code != 200 || strings.Contains(w.Body.String(), "secret-canary") || !strings.Contains(w.Body.String(), "model-a") {
			t.Fatalf("read %s: %d %s", path, w.Code, w.Body)
		}
	}
	admin := request("GET", "/v0/management/config", "admin-test-key")
	if admin.Code != 200 || !strings.Contains(admin.Body.String(), "secret-canary") {
		t.Fatal("admin read changed")
	}
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		for _, path := range []string{"/v0/management/config", "/v0/management/auth-files", "/usage-service/config", "/v0/management/api-call"} {
			if w := request(method, path, key); w.Code != 403 {
				t.Fatal(method, path, w.Code)
			}
		}
	}
	if writes != 0 {
		t.Fatal("viewer write reached upstream")
	}
}

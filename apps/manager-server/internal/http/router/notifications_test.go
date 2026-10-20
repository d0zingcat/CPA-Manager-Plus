package router

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/app"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/adminauth"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/codexinspection"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/feishunotify"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNotificationAdminBoundaryAndLocalReport(t *testing.T) {
	ctx := context.Background()
	protector, _ := security.NewProtector([]byte("local-data-key"))
	st, err := store.Open(filepath.Join(t.TempDir(), "test.sqlite"), protector)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	credential, _ := security.NewAdminCredential("cpamp_admin_test", "test")
	if err := st.SaveAdminCredential(ctx, credential); err != nil {
		t.Fatal(err)
	}
	auth := adminauth.New(config.Config{}, st)
	svc, err := feishunotify.New(st, config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	appCtx := &app.Context{Store: st, AdminAuthService: auth, FeishuNotifications: svc, CodexInspectionService: codexinspection.New(st, nil)}
	handler := New(appCtx)
	request := func(method, path, key, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if key != "" {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	rotate := request("POST", "/usage-service/readonly-key", "cpamp_admin_test", "")
	var viewer struct {
		Key string `json:"key"`
	}
	if json.Unmarshal(rotate.Body.Bytes(), &viewer) != nil || viewer.Key == "" {
		t.Fatal("viewer key creation failed")
	}
	for _, path := range []string{"/usage-service/notifications/feishu", "/usage-service/notifications/feishu/test", "/usage-service/notifications/local-inspection"} {
		method := "POST"
		if path == "/usage-service/notifications/feishu" {
			method = "GET"
		}
		if w := request(method, path, viewer.Key, `{}`); w.Code != http.StatusForbidden {
			t.Fatalf("viewer access %s: %d", path, w.Code)
		}
		if w := request(method, path, "", `{}`); w.Code != 401 {
			t.Fatalf("anonymous access %s: %d", path, w.Code)
		}
	}
	saved := request("PATCH", "/usage-service/notifications/feishu", "cpamp_admin_test", `{"enabled":true,"webhookUrl":"https://open.feishu.cn/open-apis/bot/v2/hook/test-private-token","signingSecret":"private-signing-secret","events":["auth_invalid","auth_recovered","quota_exhausted","quota_recovered"]}`)
	if saved.Code != 200 || strings.Contains(saved.Body.String(), "test-private-token") || strings.Contains(saved.Body.String(), "private-signing-secret") {
		t.Fatalf("unsafe save: %d %s", saved.Code, saved.Body)
	}
	report := func(timestamp int64, status int, failure string, used int) {
		body := fmt.Sprintf(`{"results":[{"provider":"codex","authIndex":"one","observedAtMs":%d,"statusCode":%d,"errorKind":"","failureCode":%q,"windows":[{"id":"weekly","usedPercent":%d,"resetAtMs":0,"resetAccuracy":"unknown","limitWindowSeconds":null,"modelScope":null}]}]}`, timestamp, status, failure, used)
		w := request("POST", "/usage-service/notifications/local-inspection", "cpamp_admin_test", body)
		if w.Code != 200 {
			t.Fatalf("local report: %d %s", w.Code, w.Body)
		}
		var summary codexinspection.LocalHealthReportSummary
		if err := json.Unmarshal(w.Body.Bytes(), &summary); err != nil {
			t.Fatal(err)
		}
		if !summary.Accepted || summary.AcceptedCount+summary.SkippedCount != 1 {
			t.Fatalf("missing report summary: %s", w.Body)
		}
		if timestamp > time.Now().Add(5*time.Minute).UnixMilli() && (summary.SkippedCount != 1 || summary.SkippedReasons.ClockAhead != 1) {
			t.Fatalf("missing clock warning: %s", w.Body)
		}
	}
	now := time.Now().UnixMilli()
	report(now, 401, "invalid_token", 100)
	report(now, 401, "invalid_token", 100)
	history, err := st.RecentHealthDeliveries(ctx)
	if err != nil || len(history) != 2 {
		t.Fatalf("auth/quota dedup: %d %v", len(history), err)
	}
	report(now+1, 200, "", 10)
	history, _ = st.RecentHealthDeliveries(ctx)
	if len(history) != 4 {
		t.Fatalf("recovery events: %+v", history)
	}
	report(time.Now().Add(6*time.Minute).UnixMilli(), 401, "invalid_token", 100)
	if w := request("POST", "/usage-service/notifications/local-inspection", "cpamp_admin_test", `{"results":[],"rawBody":"do not accept"}`); w.Code != 400 {
		t.Fatal("raw response report accepted")
	}
}

package feishunotify

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSettingsEncryptionReloadFilteringAndRedaction(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "settings.sqlite")
	protector, _ := security.NewProtector([]byte("test-data-key"))
	st, err := store.Open(path, protector)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc, err := New(st, config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	url := "https://open.feishu.cn/open-apis/bot/v2/hook/private-test-token"
	secret := "private-signing-secret"
	events := []string{"auth_invalid"}
	status, err := svc.Update(ctx, UpdateRequest{Enabled: &enabled, WebhookURL: &url, SigningSecret: &secret, Events: &events})
	if err != nil {
		t.Fatal(err)
	}
	public, _ := json.Marshal(status)
	if strings.Contains(string(public), "private-") || !status.WebhookConfigured || !status.SigningSecretConfigured || status.Locked {
		t.Fatalf("unsafe status %s", public)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var raw string
	if err := db.QueryRow(`select value from settings where key='feishu_notifications_v1'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, url) || strings.Contains(raw, secret) || !strings.HasPrefix(raw, "enc:v1:") {
		t.Fatal("notification settings not encrypted")
	}
	now := time.Now().UnixMilli()
	for _, o := range []model.HealthObservation{{Provider: "codex", AuthIndex: "one", Dimension: "auth", Unhealthy: true, ObservedAtMS: now}, {Provider: "codex", AuthIndex: "one", Dimension: "auth", Unhealthy: false, ObservedAtMS: now + 1}, {Provider: "codex", AuthIndex: "one", Dimension: "quota:weekly", Unhealthy: true, ObservedAtMS: now}} {
		if err := st.ObserveHealth(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	history, err := st.RecentHealthDeliveries(ctx)
	if err != nil || len(history) != 1 || history[0].Event != "auth_invalid" {
		t.Fatalf("filter: %+v %v", history, err)
	}
	again, err := New(st, config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if !again.active() || again.webhook != url || again.secret != secret {
		t.Fatal("settings did not survive reload")
	}
	blank := ""
	if _, err := again.Update(ctx, UpdateRequest{WebhookURL: &blank, SigningSecret: &blank}); err != nil {
		t.Fatal(err)
	}
	if again.webhook != url || again.secret != secret {
		t.Fatal("blank credential fields erased saved values")
	}
	if _, err := again.Update(ctx, UpdateRequest{ClearSigningSecret: true}); err != nil {
		t.Fatal(err)
	}
	if again.secret != "" {
		t.Fatal("secret clear did not apply")
	}
	locked, err := New(st, config.Config{FeishuEnvSet: true})
	if err != nil {
		t.Fatal(err)
	}
	if locked.active() {
		t.Fatal("explicit environment disable ignored")
	}
	if _, err := locked.Update(ctx, UpdateRequest{Enabled: &enabled}); err == nil {
		t.Fatal("environment lock bypassed")
	}
}
func TestDynamicTestSendingAndRateLimit(t *testing.T) {
	protector, _ := security.NewProtector([]byte("test-key"))
	st, err := store.Open(filepath.Join(t.TempDir(), "test.sqlite"), protector)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc, err := New(st, config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	// The running loop must remain usable when enabled after startup.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); svc.Run(ctx) }()
	defer func() { cancel(); <-done }()
	time.Sleep(30 * time.Millisecond)
	enabled := true
	url := "https://open.feishu.cn/open-apis/bot/v2/hook/test-token"
	if _, err := svc.Update(ctx, UpdateRequest{Enabled: &enabled, WebhookURL: &url}); err != nil {
		t.Fatal(err)
	}
	deliveries := make(chan struct{}, 4)
	svc.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		deliveries <- struct{}{}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"code":0}`))}, nil
	})
	if err := svc.SendTest(ctx); err != nil {
		t.Fatal(err)
	}
	if err := svc.SendTest(ctx); !errors.Is(err, ErrTestRateLimited) {
		t.Fatalf("test throttle: %v", err)
	}
	<-deliveries
	if err := st.ObserveHealth(ctx, model.HealthObservation{Provider: "codex", AuthIndex: "hot-enabled", Dimension: "auth", Unhealthy: true, ObservedAtMS: time.Now().UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-deliveries:
	case <-time.After(3 * time.Second):
		t.Fatal("sender did not start delivering after page enabled notifications")
	}
	status, err := svc.Status(ctx)
	if err != nil || len(status.Deliveries) != 2 || status.Deliveries[1].Event != "test" || status.Deliveries[1].Status != "sent" {
		t.Fatalf("missing test history: %+v %v", status, err)
	}
}
func TestMissingProtectorDoesNotActivateUnsavedSettings(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc, _ := New(st, config.Config{})
	enabled := true
	url := "https://open.feishu.cn/open-apis/bot/v2/hook/test-token"
	if _, err := svc.Update(context.Background(), UpdateRequest{Enabled: &enabled, WebhookURL: &url}); err == nil || svc.active() {
		t.Fatal("failed save changed runtime")
	}
}

func TestStoredConfigurationFailureDegradesAndCannotBeOverwritten(t *testing.T) {
	ctx := context.Background()
	protector, _ := security.NewProtector([]byte("original-key"))
	path := filepath.Join(t.TempDir(), "broken.sqlite")
	st, err := store.Open(path, protector)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Settings.SaveFeishuSettings(ctx, model.FeishuSettings{Enabled: true, WebhookURL: "https://open.feishu.cn/open-apis/bot/v2/hook/test-token", UpdatedAtMS: 1}); err != nil {
		t.Fatal(err)
	}
	st.Close()
	wrong, _ := security.NewProtector([]byte("wrong-key"))
	st, err = store.Open(path, wrong)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc, err := New(st, config.Config{})
	if err != nil || svc == nil {
		t.Fatalf("optional config prevented startup: %v", err)
	}
	status, err := svc.Status(ctx)
	if err != nil || status.Enabled || status.ConfigurationError == "" || st.HealthNotificationsActive() {
		t.Fatalf("missing degraded status: %+v %v", status, err)
	}
	if _, err := svc.Update(ctx, UpdateRequest{}); err == nil {
		t.Fatal("corrupted config can be overwritten")
	}
}

func TestInvalidStoredWebhookAndExplicitClear(t *testing.T) {
	ctx := context.Background()
	protector, _ := security.NewProtector([]byte("test-key"))
	st, err := store.Open(filepath.Join(t.TempDir(), "invalid.sqlite"), protector)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Settings.SaveFeishuSettings(ctx, model.FeishuSettings{Enabled: true, WebhookURL: "invalid", UpdatedAtMS: 1}); err != nil {
		t.Fatal(err)
	}
	svc, err := New(st, config.Config{})
	if err != nil || svc.active() {
		t.Fatalf("invalid DB settings prevented startup: %v", err)
	}
	if _, err := New(st, config.Config{FeishuEnabled: true, FeishuWebhookURL: "invalid"}); err == nil {
		t.Fatal("invalid env settings accepted")
	}
	if err := st.Settings.SaveFeishuSettings(ctx, model.FeishuSettings{Enabled: true, WebhookURL: "https://open.feishu.cn/open-apis/bot/v2/hook/test-token", SigningSecret: "secret", UpdatedAtMS: 2}); err != nil {
		t.Fatal(err)
	}
	svc, err = New(st, config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	status, err := svc.Update(ctx, UpdateRequest{ClearWebhook: true})
	if err != nil || status.Enabled || status.WebhookConfigured || status.SigningSecretConfigured {
		t.Fatalf("clear failed: %+v %v", status, err)
	}
	reloaded, err := New(st, config.Config{})
	if err != nil || reloaded.active() || reloaded.settings.WebhookURL != "" {
		t.Fatal("clear did not persist")
	}
}

func TestInvalidStoredWebhookCanBeRepairedFromSettings(t *testing.T) {
	for _, clear := range []bool{false, true} {
		t.Run(map[bool]string{false: "replace", true: "clear"}[clear], func(t *testing.T) {
			ctx := context.Background()
			protector, _ := security.NewProtector([]byte("repair-key"))
			st, err := store.Open(filepath.Join(t.TempDir(), "repair.sqlite"), protector)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			if err := st.Settings.SaveFeishuSettings(ctx, model.FeishuSettings{Enabled: true, WebhookURL: "invalid", SigningSecret: "retained-secret", UpdatedAtMS: 1}); err != nil {
				t.Fatal(err)
			}
			svc, err := New(st, config.Config{})
			if err != nil {
				t.Fatal(err)
			}
			initial, err := svc.Status(ctx)
			if err != nil || initial.ConfigurationError != "stored_notification_settings_invalid" {
				t.Fatal("validation and decryption errors not distinguished")
			}
			enabled := true
			url := "https://open.feishu.cn/open-apis/bot/v2/hook/repaired-token"
			status, err := svc.Update(ctx, UpdateRequest{Enabled: &enabled, WebhookURL: &url, ClearWebhook: clear})
			if err != nil || status.ConfigurationError != "" || status.Enabled == clear {
				t.Fatalf("repair failed: %+v %v", status, err)
			}
			reloaded, err := New(st, config.Config{})
			if err != nil || reloaded.configError != "" || reloaded.active() == clear {
				t.Fatal("repair did not survive reload")
			}
			if !clear && reloaded.settings.SigningSecret != "retained-secret" {
				t.Fatal("repair discarded readable signing secret")
			}
		})
	}
}

func TestUnreadableSettingsRequireExplicitCompleteReplacement(t *testing.T) {
	ctx := context.Background()
	original, _ := security.NewProtector([]byte("old-key"))
	path := filepath.Join(t.TempDir(), "replace.sqlite")
	st, err := store.Open(path, original)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Settings.SaveFeishuSettings(ctx, model.FeishuSettings{Enabled: true, WebhookURL: "https://open.feishu.cn/open-apis/bot/v2/hook/original", SigningSecret: "old-secret", UpdatedAtMS: 1}); err != nil {
		t.Fatal(err)
	}
	st.Close()
	replacement, _ := security.NewProtector([]byte("new-key"))
	st, err = store.Open(path, replacement)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc, err := New(st, config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	url := "https://open.feishu.cn/open-apis/bot/v2/hook/replacement"
	events := []string{"auth_invalid"}
	emptySecret := ""
	req := UpdateRequest{Enabled: &enabled, WebhookURL: &url, Events: &events, SigningSecret: &emptySecret}
	if _, err := svc.Update(ctx, req); err == nil {
		t.Fatal("implicit overwrite allowed")
	}
	if _, err := svc.Update(ctx, UpdateRequest{ReplaceConfiguration: true, WebhookURL: &url}); err == nil {
		t.Fatal("partial replacement allowed")
	}
	req.ReplaceConfiguration = true
	status, err := svc.Update(ctx, req)
	if err != nil || !status.Enabled || status.ConfigurationError != "" || status.SigningSecretConfigured {
		t.Fatalf("explicit replacement failed: %+v %v", status, err)
	}
	reloaded, err := New(st, config.Config{})
	if err != nil || !reloaded.active() || reloaded.configError != "" || reloaded.settings.SigningSecret != "" {
		t.Fatal("replacement not encrypted with current key")
	}
	// Explicit clearing also works when no old values can be decrypted.
	otherKey, _ := security.NewProtector([]byte("another-key"))
	other, err := store.Open(path, otherKey)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	broken, err := New(other, config.Config{})
	if err != nil || broken.configError == "" {
		t.Fatal("expected unreadable settings")
	}
	cleared, err := broken.Update(ctx, UpdateRequest{ClearWebhook: true})
	if err != nil || cleared.Enabled || cleared.WebhookConfigured || cleared.SigningSecretConfigured || cleared.ConfigurationError != "" {
		t.Fatalf("explicit unreadable reset failed: %+v %v", cleared, err)
	}
	clean, err := New(other, config.Config{})
	if err != nil || clean.configError != "" || clean.active() {
		t.Fatal("cleared unreadable settings did not reload")
	}
}

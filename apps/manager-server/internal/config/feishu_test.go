package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFeishuConfigSecretsAndDefaultOff(t *testing.T) {
	clearConfigEnv(t)
	for _, key := range []string{"CPA_MANAGER_FEISHU_ENABLED", "CPA_MANAGER_FEISHU_WEBHOOK_URL", "CPA_MANAGER_FEISHU_WEBHOOK_URL_FILE", "CPA_MANAGER_FEISHU_SIGNING_SECRET", "CPA_MANAGER_FEISHU_SIGNING_SECRET_FILE"} {
		t.Setenv(key, "")
	}
	dir := t.TempDir()
	t.Setenv(configEnvKey, filepath.Join(dir, "absent.json"))
	cfg, err := LoadWithoutCreatingDefault()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FeishuEnabled {
		t.Fatal("notifications enabled by default")
	}
	t.Setenv("CPA_MANAGER_FEISHU_ENABLED", "true")
	webhookFile := filepath.Join(dir, "webhook")
	secretFile := filepath.Join(dir, "secret")
	if err := os.WriteFile(webhookFile, []byte("https://open.feishu.cn/open-apis/bot/v2/hook/test-secret-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secretFile, []byte("test-signing-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CPA_MANAGER_FEISHU_WEBHOOK_URL_FILE", webhookFile)
	t.Setenv("CPA_MANAGER_FEISHU_SIGNING_SECRET_FILE", secretFile)
	cfg, err = LoadWithoutCreatingDefault()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FeishuSigningSecret != "test-signing-secret" || !strings.HasSuffix(cfg.FeishuWebhookURL, "test-secret-token") {
		t.Fatal("secret file values not loaded")
	}
	public, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(public), "test-secret-token") || strings.Contains(string(public), "test-signing-secret") {
		t.Fatal("secrets exposed by config serialization")
	}
	t.Setenv("CPA_MANAGER_FEISHU_SIGNING_SECRET_FILE", filepath.Join(dir, "missing"))
	if _, err := LoadWithoutCreatingDefault(); err == nil {
		t.Fatal("unreadable signing file silently disabled signing")
	}
	t.Setenv("CPA_MANAGER_FEISHU_SIGNING_SECRET", "env-signing-secret")
	cfg, err = LoadWithoutCreatingDefault()
	if err != nil || cfg.FeishuSigningSecret != "env-signing-secret" {
		t.Fatal("explicit secret did not take precedence")
	}
}

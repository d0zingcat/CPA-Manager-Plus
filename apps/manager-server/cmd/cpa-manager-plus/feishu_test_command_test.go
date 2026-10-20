package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

func TestFeishuCLIReadsPageSettingsWithoutWritingDatabase(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{DBPath: filepath.Join(dir, "manager.sqlite"), DataKeyPath: filepath.Join(dir, "data.key")}
	key, _, err := security.LoadOrCreateDataKey("", cfg.DataKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	protector, _ := security.NewProtector(key)
	st, err := store.Open(cfg.DBPath, protector)
	if err != nil {
		t.Fatal(err)
	}
	saved := model.FeishuSettings{Enabled: true, WebhookURL: "https://open.feishu.cn/open-apis/bot/v2/hook/test-token", SigningSecret: "test-secret", UpdatedAtMS: 1}
	if err := st.Settings.SaveFeishuSettings(context.Background(), saved); err != nil {
		t.Fatal(err)
	}
	st.Close()
	before, err := os.ReadFile(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	effective, err := loadFeishuTestConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !effective.FeishuEnabled || effective.FeishuWebhookURL != saved.WebhookURL || effective.FeishuSigningSecret != saved.SigningSecret {
		t.Fatal("CLI ignored stored page settings")
	}
	after, err := os.ReadFile(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("CLI modified database")
	}
	// An explicit environment disable wins even when the database is unavailable.
	cfg.FeishuEnvSet = true
	cfg.DBPath = filepath.Join(dir, "missing.sqlite")
	effective, err = loadFeishuTestConfig(cfg)
	if err != nil || effective.FeishuEnabled {
		t.Fatal("environment precedence lost")
	}
	cfg.FeishuEnvSet = false
	if _, err := loadFeishuTestConfig(cfg); err == nil {
		t.Fatal("missing DB accepted")
	}
	if _, err := os.Stat(cfg.DBPath); !os.IsNotExist(err) {
		t.Fatal("CLI created a database")
	}
}

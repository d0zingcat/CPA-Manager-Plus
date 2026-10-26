package main

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"path/filepath"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	settingrepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/setting"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/feishunotify"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

func newFeishuTestSender(cfg config.Config) (*feishunotify.Service, error) {
	effective, err := loadFeishuTestConfig(cfg)
	if err != nil {
		return nil, err
	}
	return feishunotify.New(&store.Store{}, effective)
}

func loadFeishuTestConfig(cfg config.Config) (config.Config, error) {
	if cfg.FeishuEnvSet || cfg.FeishuEnabled || cfg.FeishuWebhookURL != "" || cfg.FeishuSigningSecret != "" {
		return cfg, nil
	}
	// mode=ro prevents accidental creation; settings.New performs no migrations.
	path, err := filepath.Abs(cfg.DBPath)
	if err != nil {
		return config.Config{}, errors.New("invalid manager database path")
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return config.Config{}, errors.New("cannot open existing notification settings")
	}
	defer db.Close()
	key, err := security.LoadDataKey(cfg.DataKey, cfg.DataKeyPath)
	if err != nil {
		return config.Config{}, errors.New("cannot read existing notification data key")
	}
	protector, err := security.NewProtector(key)
	if err != nil {
		return config.Config{}, errors.New("cannot initialize notification data key")
	}
	settings, err := settingrepo.New(db, protector).LoadFeishuSettings(context.Background())
	if err != nil {
		return config.Config{}, err
	}
	cfg.FeishuEnvSet = true
	cfg.FeishuEnabled = settings.Enabled
	cfg.FeishuWebhookURL = settings.WebhookURL
	cfg.FeishuSigningSecret = settings.SigningSecret
	// The explicit CLI test writes no history into the read-only database.
	return cfg, nil
}

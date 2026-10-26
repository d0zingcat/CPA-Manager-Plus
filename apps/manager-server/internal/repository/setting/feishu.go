package setting

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
)

const feishuKey = "feishu_notifications_v1"

func (r *repository) LoadFeishuSettings(ctx context.Context) (model.FeishuSettings, error) {
	var cfg model.FeishuSettings
	var raw string
	err := r.db.QueryRowContext(ctx, `select value from settings where key=?`, feishuKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return cfg, nil
	}
	if err != nil {
		return cfg, errors.New("cannot load notification settings")
	}
	if r.protector == nil {
		return cfg, errors.New("notification secret protector unavailable")
	}
	plain, err := r.protector.UnprotectString(raw)
	if err != nil {
		return cfg, errors.New("cannot decrypt notification settings")
	}
	if err := json.Unmarshal([]byte(plain), &cfg); err != nil {
		return cfg, errors.New("invalid stored notification settings")
	}
	return cfg, nil
}
func (r *repository) SaveFeishuSettings(ctx context.Context, cfg model.FeishuSettings) error {
	if r.protector == nil {
		return errors.New("notification secret protector unavailable")
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return errors.New("cannot encode notification settings")
	}
	sealed, err := r.protector.ProtectString(string(raw))
	if err != nil {
		return errors.New("cannot encrypt notification settings")
	}
	_, err = r.db.ExecContext(ctx, `insert into settings(key,value,updated_at_ms) values(?,?,?) on conflict(key) do update set value=excluded.value,updated_at_ms=excluded.updated_at_ms`, feishuKey, sealed, cfg.UpdatedAtMS)
	if err != nil {
		return errors.New("cannot save notification settings")
	}
	return nil
}

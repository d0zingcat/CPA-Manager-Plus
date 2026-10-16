package setting

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
)

// Readonly credentials are independent of bootstrap and the administrator key.
const readonlyCredentialKey = "usage_viewer_credential_v1"

func (r *repository) SaveReadonlyCredential(ctx context.Context, credential model.AdminCredential) error {
	data, err := json.Marshal(credential)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `insert into settings(key,value,updated_at_ms) values(?,?,?) on conflict(key) do update set value=excluded.value, updated_at_ms=excluded.updated_at_ms`, readonlyCredentialKey, string(data), time.Now().UnixMilli())
	return err
}

func (r *repository) LoadReadonlyCredential(ctx context.Context) (model.AdminCredential, bool, error) {
	var raw string
	err := r.db.QueryRowContext(ctx, `select value from settings where key=?`, readonlyCredentialKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return model.AdminCredential{}, false, nil
	}
	if err != nil {
		return model.AdminCredential{}, false, err
	}
	var credential model.AdminCredential
	if err := json.Unmarshal([]byte(raw), &credential); err != nil {
		return model.AdminCredential{}, false, err
	}
	return credential, credential.KeyHash != "", nil
}

package adminauth

import (
	"context"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

type Role string

const (
	RoleAdmin       Role = "admin"
	RoleUsageViewer Role = "usage_viewer"
)

func (s *Service) Role(ctx context.Context, header string) (Role, error) {
	ok, err := s.VerifyHeader(ctx, header)
	if err != nil {
		return "", err
	}
	if ok {
		return RoleAdmin, nil
	}
	credential, enabled, err := s.store.LoadReadonlyCredential(ctx)
	if err != nil {
		return "", err
	}
	if enabled && security.VerifyAdminKey(credential, security.ExtractBearerToken(header)) {
		return RoleUsageViewer, nil
	}
	return "", nil
}

type ReadonlyKeyStatus struct {
	Enabled     bool  `json:"enabled"`
	UpdatedAtMS int64 `json:"updatedAtMs"`
}

func (s *Service) ReadonlyKeyStatus(ctx context.Context) (ReadonlyKeyStatus, error) {
	credential, enabled, err := s.store.LoadReadonlyCredential(ctx)
	return ReadonlyKeyStatus{Enabled: enabled, UpdatedAtMS: credential.RotatedAtMS}, err
}

func (s *Service) RotateReadonlyKey(ctx context.Context) (string, error) {
	key, err := security.GenerateAdminKey()
	if err != nil {
		return "", err
	}
	key = "cpamp_view_" + strings.TrimPrefix(key, "cpamp_")
	credential, err := security.NewAdminCredential(key, "usage-viewer")
	if err != nil {
		return "", err
	}
	credential.RotatedAtMS = time.Now().UnixMilli()
	if err := s.store.SaveReadonlyCredential(ctx, credential); err != nil {
		return "", err
	}
	return key, nil
}

func (s *Service) DisableReadonlyKey(ctx context.Context) error {
	return s.store.SaveReadonlyCredential(ctx, store.AdminCredential{RotatedAtMS: time.Now().UnixMilli()})
}

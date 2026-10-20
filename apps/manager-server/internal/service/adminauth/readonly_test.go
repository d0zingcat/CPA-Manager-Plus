package adminauth

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
)

func TestReadonlyCredentialStoresOnlyHashAndRemainsIndependent(t *testing.T) {
	st := newResetTestStore(t)
	ctx := context.Background()
	_, err := ResetAdminKey(ctx, st, "cpamp_admin")
	if err != nil {
		t.Fatal(err)
	}
	service := New(config.Config{}, st)
	key, err := service.RotateReadonlyKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	credential, enabled, err := st.LoadReadonlyCredential(ctx)
	if err != nil || !enabled {
		t.Fatalf("load: enabled=%v err=%v", enabled, err)
	}
	raw, err := json.Marshal(credential)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), key) || credential.KeyHash == "" || credential.Salt == "" {
		t.Fatal("plaintext or incomplete credential persisted")
	}
	if !security.VerifyAdminKey(credential, key) {
		t.Fatal("persisted hash cannot verify")
	}
	if _, err := ResetAdminKey(ctx, st, "cpamp_admin_new"); err != nil {
		t.Fatal(err)
	}
	role, err := service.Role(ctx, "Bearer "+key)
	if err != nil || role != RoleUsageViewer {
		t.Fatal("admin rotation changed viewer")
	}
	// A storage failure must not authenticate a caller or return a generated key.
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if role, err := service.Role(ctx, "Bearer "+key); err == nil || role != "" {
		t.Fatal("storage error did not fail closed")
	}
	if key, err := service.RotateReadonlyKey(ctx); err == nil || key != "" {
		t.Fatal("failed persistence disclosed a key")
	}
}

package quotasnapshot

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

func TestQuotaNotificationsIsolateWindowsScopesAndOldEvidence(t *testing.T) {
	st := testutil.NewStore(t, testutil.NewConfig(t))
	st.ConfigureHealthNotifications(true, []string{"auth_invalid", "auth_recovered", "quota_exhausted", "quota_recovered"})
	svc := New(st)
	ctx := context.Background()
	now := time.Now().UnixMilli()
	used := 100.0
	entry := WriteEntry{Provider: "codex", Account: AccountTarget{AuthIndex: "auth-one", AuthFileSnapshot: "a.json"}, Windows: []WindowInput{{ProviderWindowID: "weekly", WindowKind: "weekly", WindowMode: "unknown", ModelScopeKind: "all", Source: "api_query", ObservedAtMS: now, BoundaryAccuracy: "unknown", UsedPercent: &used}}}
	write := func(e WriteEntry) {
		t.Helper()
		if _, err := svc.Write(ctx, WriteRequest{Entries: []WriteEntry{e}}); err != nil {
			t.Fatal(err)
		}
	}
	next := func() *model.HealthObservation {
		t.Helper()
		n, ok, err := st.ClaimHealthNotification(ctx, time.Now().UnixMilli()+1000)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			return nil
		}
		var o model.HealthObservation
		if err := json.Unmarshal([]byte(n.Payload), &o); err != nil {
			t.Fatal(err)
		}
		if err := st.FinishHealthNotification(ctx, n, "", false, time.Now().UnixMilli()); err != nil {
			t.Fatal(err)
		}
		return &o
	}
	write(entry)
	write(entry)
	if o := next(); o == nil || !o.Unhealthy || o.Window != "weekly" {
		t.Fatalf("missing exhaustion: %+v", o)
	}
	if next() != nil {
		t.Fatal("duplicate exhaustion")
	}
	// Partial evidence for another window or scope is not account-wide recovery.
	used = 10
	entry.Windows[0].ObservedAtMS = now + 1
	entry.Windows[0].ProviderWindowID = "five-hour"
	entry.Windows[0].WindowKind = "five_hour"
	write(entry)
	entry.Windows[0].ProviderWindowID = "weekly"
	entry.Windows[0].WindowKind = "weekly"
	entry.Windows[0].ModelScopeKind = "feature"
	entry.Windows[0].ModelScopeKey = "spark"
	write(entry)
	if next() != nil {
		t.Fatal("cross-window/scope recovery")
	}
	entry.Windows[0].ModelScopeKind = "all"
	entry.Windows[0].ModelScopeKey = ""
	entry.Windows[0].ObservedAtMS = now - 1
	write(entry)
	if next() != nil {
		t.Fatal("stale recovery")
	}
	entry.Windows[0].ObservedAtMS = now + 2
	entry.Windows[0].UsedPercent = nil
	write(entry)
	if next() != nil {
		t.Fatal("missing data recovered quota")
	}
	entry.Windows[0].UsedPercent = &used
	entry.Windows[0].ObservedAtMS = now + 3
	write(entry)
	if o := next(); o == nil || o.Unhealthy {
		t.Fatalf("missing genuine recovery: %+v", o)
	}
	// Historical replays cannot start a new incident.
	used = 100
	entry.Windows[0].ObservedAtMS = now - (24 * time.Hour).Milliseconds()
	write(entry)
	if next() != nil {
		t.Fatal("historical evidence generated alert")
	}
}

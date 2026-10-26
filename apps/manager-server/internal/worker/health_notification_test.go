package worker

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	collectorpkg "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	quotasnapshotsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/quotasnapshot"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usage"
)

func drainHealth(t *testing.T, st *store.Store) []model.HealthObservation {
	t.Helper()
	ctx := context.Background()
	var observations []model.HealthObservation
	for {
		n, ok, err := st.ClaimHealthNotification(ctx, time.Now().UnixMilli()+1000)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		var o model.HealthObservation
		if err := json.Unmarshal([]byte(n.Payload), &o); err != nil {
			t.Fatal(err)
		}
		observations = append(observations, o)
		if err := st.FinishHealthNotification(ctx, n, "", false, time.Now().UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	return observations
}

func TestHealthWorkerEvidenceAndRecovery(t *testing.T) {
	st := testutil.NewStore(t, testutil.NewConfig(t))
	st.ConfigureHealthNotifications(true, []string{"auth_invalid", "auth_recovered", "quota_exhausted", "quota_recovered"})
	worker := NewHealthNotificationWorker(st)
	ctx := context.Background()
	now := time.Now().UnixMilli()
	base := usage.Event{Provider: "codex", AuthIndex: "account-a", AccountSnapshot: "user@example.com", AuthFileSnapshot: "a.json", TimestampMS: now, Failed: true, FailStatusCode: 401, FailSummary: "invalid_token"}
	worker.HandleUsageEvents(ctx, collectorpkg.RuntimeConfig{}, []usage.Event{base, base})
	messages := drainHealth(t, st)
	if len(messages) != 1 || messages[0].Dimension != "auth" || !messages[0].Unhealthy {
		t.Fatalf("invalid auth notification: %+v", messages)
	}
	if messages[0].Email != "user@example.com" || messages[0].Source != "usage" || messages[0].StatusCode != 401 || messages[0].ReasonCode != "invalid_credentials" {
		t.Fatalf("missing account context: %+v", messages[0])
	}
	// A network error, ambiguous 401/403, or ordinary rate limit is not recovery
	// and not a new re-login or quota-exhaustion alert.
	for i, status := range []int{0, 401, 403, 429, 500} {
		e := base
		e.TimestampMS = now + int64(i+1)
		e.FailStatusCode = status
		e.FailSummary = "upstream unavailable"
		worker.HandleUsageEvents(ctx, collectorpkg.RuntimeConfig{}, []usage.Event{e})
	}
	if got := drainHealth(t, st); len(got) != 0 {
		t.Fatalf("false-positive alerts: %+v", got)
	}
	success := base
	success.TimestampMS = now + 10
	success.Failed = false
	success.FailStatusCode = 0
	success.FailSummary = ""
	worker.HandleUsageEvents(ctx, collectorpkg.RuntimeConfig{}, []usage.Event{success})
	messages = drainHealth(t, st)
	if len(messages) != 1 || messages[0].Unhealthy || messages[0].StatusCode != 0 {
		t.Fatalf("missing recovery: %+v", messages)
	}
	// Stale replay and another account's success must not recover this account.
	quota := base
	quota.TimestampMS = now + 20
	quota.FailStatusCode = 429
	quota.FailSummary = ""
	quota.FailBody = `{"error":{"type":"usage_limit_reached"}}`
	worker.HandleUsageEvents(ctx, collectorpkg.RuntimeConfig{}, []usage.Event{quota, quota, success})
	messages = drainHealth(t, st)
	if len(messages) != 1 || messages[0].Window != "unknown" || !messages[0].Unhealthy || messages[0].ResetAtMS != 0 {
		t.Fatalf("structured exhaustion: %+v", messages)
	}
	if messages[0].Email != "user@example.com" || messages[0].Source != "usage" || messages[0].StatusCode != 429 {
		t.Fatalf("missing quota context: %+v", messages[0])
	}
	success.TimestampMS = now + 30
	worker.HandleUsageEvents(ctx, collectorpkg.RuntimeConfig{}, []usage.Event{success})
	if got := drainHealth(t, st); len(got) != 0 {
		t.Fatalf("success wrongly cleared quota: %+v", got)
	}
}

func TestHeaderQuotaDoesNotDuplicateScopedSnapshotNotification(t *testing.T) {
	st := testutil.NewStore(t, testutil.NewConfig(t))
	st.ConfigureHealthNotifications(true, []string{"auth_invalid", "auth_recovered", "quota_exhausted", "quota_recovered"})
	ctx := context.Background()
	used := 100.0
	minutes := 10080.0
	event := usage.Event{Provider: "codex", AuthIndex: "spark-one", AccountSnapshot: "quota@example.com", AuthFileSnapshot: "spark.json", TimestampMS: time.Now().UnixMilli(), Failed: true, FailStatusCode: 429, Model: "gpt-5.3-codex-spark", FailBody: `{"error":{"type":"usage_limit_reached"}}`, ResponseMetadata: &usage.ResponseHeaderMetadata{Quota: &usage.HeaderQuotaMetadata{Primary: &usage.HeaderQuotaWindow{UsedPercent: &used, WindowMinutes: &minutes}}}}
	if err := quotasnapshotsvc.New(st).WriteUsageEvents(ctx, []usage.Event{event}); err != nil {
		t.Fatal(err)
	}
	NewHealthNotificationWorker(st).HandleUsageEvents(ctx, collectorpkg.RuntimeConfig{}, []usage.Event{event})
	messages := drainHealth(t, st)
	if len(messages) != 1 || messages[0].Window != "spark-weekly-0" || messages[0].Email != "quota@example.com" || messages[0].Source != "quota_snapshot" {
		t.Fatalf("scoped snapshot/fallback duplication: %+v", messages)
	}
}

func TestHealthNotificationRejectsFilenameEmailFallback(t *testing.T) {
	for _, path := range []string{"auth", "quota-error", "quota-snapshot"} {
		t.Run(path, func(t *testing.T) {
			st := testutil.NewStore(t, testutil.NewConfig(t))
			st.ConfigureHealthNotifications(true, []string{"auth_invalid", "quota_exhausted"})
			event := usage.Event{Provider: "codex", AuthIndex: "one", AccountSnapshot: "codex-user@example.com-plus.json", AuthFileSnapshot: "codex-user@example.com-plus.json", TimestampMS: time.Now().UnixMilli(), Failed: true, FailStatusCode: 401, FailSummary: "invalid_token"}
			ctx := context.Background()
			switch path {
			case "quota-error":
				event.FailStatusCode = 429
				event.FailSummary = ""
				event.FailBody = `{"error":{"type":"usage_limit_reached"}}`
			case "quota-snapshot":
				used, minutes := 100.0, 10080.0
				event.ResponseMetadata = &usage.ResponseHeaderMetadata{Quota: &usage.HeaderQuotaMetadata{Primary: &usage.HeaderQuotaWindow{UsedPercent: &used, WindowMinutes: &minutes}}}
				if err := quotasnapshotsvc.New(st).WriteUsageEvents(ctx, []usage.Event{event}); err != nil {
					t.Fatal(err)
				}
			}
			if path != "quota-snapshot" {
				NewHealthNotificationWorker(st).HandleUsageEvents(ctx, collectorpkg.RuntimeConfig{}, []usage.Event{event})
			}
			messages := drainHealth(t, st)
			if len(messages) != 1 || messages[0].Email != "" {
				t.Fatalf("file name used as email through %s: %+v", path, messages)
			}
		})
	}
}

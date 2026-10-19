package codexinspection

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usageidentity"
)

func TestInspectionHealthNotifications(t *testing.T) {
	st := testutil.NewStore(t, testutil.NewConfig(t))
	st.ConfigureHealthNotifications(true, []string{"auth_invalid", "auth_recovered", "quota_exhausted", "quota_recovered"})
	svc := &Service{store: st}
	ctx := context.Background()
	now := time.Now().UnixMilli()
	unauthorized := 401
	success := 200
	result := model.CodexInspectionResult{Provider: "xai", AuthIndex: "xai-one", AccountSnapshot: "inspection@example.com", Action: "reauth", ErrorDetail: "invalid_token", StatusCode: &unauthorized, CreatedAtMS: now}
	svc.observeInspectionHealth(ctx, result)
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
	if o := next(); o == nil || !o.Unhealthy || o.Email != "inspection@example.com" || o.Source != "inspection" || o.StatusCode != 401 || o.ReasonCode != "invalid_credentials" {
		t.Fatal("missing reauth")
	}
	result.CreatedAtMS++
	result.AuthIndex = "ambiguous-account"
	result.ErrorDetail = "unauthorized"
	svc.observeInspectionHealth(ctx, result)
	if next() != nil {
		t.Fatal("generic inspection 401 triggered re-login alert")
	}
	result.AuthIndex = "xai-one"
	result.Action = "keep"
	result.CreatedAtMS++
	result.StatusCode = &success
	result.ErrorKind = "billing_partial"
	svc.observeInspectionHealth(ctx, result)
	if next() != nil {
		t.Fatal("partial billing recovered authentication")
	}
	result.CreatedAtMS++
	result.ErrorKind = "official_api_healthy"
	svc.observeInspectionHealth(ctx, result)
	if o := next(); o == nil || o.Unhealthy {
		t.Fatal("missing explicit auth recovery")
	}
	used := 100.0
	result.CreatedAtMS++
	result.QuotaWindows = []model.CodexInspectionQuotaWindow{{ID: "xai-weekly", UsedPercent: &used}}
	svc.observeInspectionHealth(ctx, result)
	if o := next(); o == nil || !o.Unhealthy || o.Window != "xai-weekly" {
		t.Fatal("missing xai quota exhaustion")
	}
	for _, bad := range []float64{-1, 101, math.NaN(), math.Inf(1)} {
		used = bad
		result.CreatedAtMS++
		svc.observeInspectionHealth(ctx, result)
	}
	if next() != nil {
		t.Fatal("invalid percentage triggered recovery")
	}
	used = 10
	result.CreatedAtMS++
	svc.observeInspectionHealth(ctx, result)
	if o := next(); o == nil || o.Unhealthy {
		t.Fatal("missing xai quota recovery")
	}
	used = 100
	result.CreatedAtMS++
	result.QuotaWindows[0].ID = "xai-product-0"
	svc.observeInspectionHealth(ctx, result)
	if next() != nil {
		t.Fatal("unstable product row triggered notification")
	}
}

func TestLocalAndServerInspectionShareNotificationState(t *testing.T) {
	st := testutil.NewStore(t, testutil.NewConfig(t))
	st.ConfigureHealthNotifications(true, []string{"auth_invalid", "auth_recovered", "quota_exhausted", "quota_recovered"})
	svc := New(st, nil)
	ctx := context.Background()
	now := time.Now().UnixMilli()
	used := 100.0
	local := LocalHealthResult{Provider: "codex", AuthIndex: "one", ObservedAtMS: now, StatusCode: 401, FailureCode: "invalid_token", Windows: []LocalHealthWindow{{ID: "weekly", UsedPercent: &used, ResetAccuracy: "unknown"}}}
	if _, err := svc.ReportLocalHealth(ctx, []LocalHealthResult{local}); err != nil {
		t.Fatal(err)
	}
	status := 401
	server := model.CodexInspectionResult{Provider: "codex", AuthIndex: "one", CreatedAtMS: now + 1, StatusCode: &status, Action: "reauth", ErrorDetail: "invalid_token", QuotaWindows: []model.CodexInspectionQuotaWindow{{ID: "weekly", UsedPercent: &used, ResetAccuracy: "unknown"}}}
	svc.observeInspectionHealth(ctx, server)
	if err := svc.quotaSnapshots.WriteCodexInspectionResult(ctx, server); err != nil {
		t.Fatal(err)
	}
	history, err := st.RecentHealthDeliveries(ctx)
	if err != nil || len(history) != 2 {
		t.Fatalf("local/server duplicated notifications: %+v %v", history, err)
	}
}

func TestLocalInspectionOlderThanFifteenMinutes(t *testing.T) {
	st := testutil.NewStore(t, testutil.NewConfig(t))
	st.ConfigureHealthNotifications(true, []string{"auth_invalid", "quota_exhausted"})
	svc := New(st, nil)
	used := 100.0
	local := LocalHealthResult{Provider: "codex", AuthIndex: "long-local", ObservedAtMS: time.Now().Add(-45 * time.Minute).UnixMilli(), StatusCode: 401, FailureCode: "invalid_token", Windows: []LocalHealthWindow{{ID: "weekly", UsedPercent: &used, ResetAccuracy: "unknown"}}}
	if _, err := svc.ReportLocalHealth(context.Background(), []LocalHealthResult{local}); err != nil {
		t.Fatal(err)
	}
	history, err := st.RecentHealthDeliveries(context.Background())
	if err != nil || len(history) != 2 {
		t.Fatalf("long local results were lost: %+v %v", history, err)
	}
}

func TestLocalQuotaHistoryDoesNotDependOnNotificationSwitch(t *testing.T) {
	st := testutil.NewStore(t, testutil.NewConfig(t))
	svc := New(st, nil)
	ctx := context.Background()
	key, ok := usageidentity.AccountKey(usageidentity.Fields{AuthProviderSnapshot: "codex", AuthIndex: "local-history"})
	if !ok {
		t.Fatal("missing account identity")
	}
	used := 60.0
	local := LocalHealthResult{Provider: "codex", AuthIndex: "local-history", ObservedAtMS: time.Now().Add(-time.Hour).UnixMilli(), StatusCode: 200, Windows: []LocalHealthWindow{{ID: "weekly", UsedPercent: &used, ResetAccuracy: "unknown"}}}
	for _, enabled := range []bool{false, true, false} {
		st.ConfigureHealthNotifications(enabled, []string{"quota_exhausted"})
		local.ObservedAtMS += 1000
		if _, err := svc.ReportLocalHealth(ctx, []LocalHealthResult{local}); err != nil {
			t.Fatal(err)
		}
		snapshots, err := st.QuotaSnapshots.ListCandidates(ctx, key, "codex", 100)
		if err != nil || len(snapshots) == 0 {
			t.Fatalf("quota history lost with enabled=%v: %v", enabled, err)
		}
		latest := int64(0)
		for _, snapshot := range snapshots {
			if snapshot.ObservedAtMS > latest {
				latest = snapshot.ObservedAtMS
			}
		}
		if latest != local.ObservedAtMS {
			t.Fatalf("local snapshot was deduplicated across runs: got %d want %d", latest, local.ObservedAtMS)
		}
	}
}

func TestServerDelayedInspectionPreservesCreationTime(t *testing.T) {
	st := testutil.NewStore(t, testutil.NewConfig(t))
	st.ConfigureHealthNotifications(true, []string{"auth_invalid"})
	svc := New(st, nil)
	ctx := context.Background()
	old := time.Now().Add(-45 * time.Minute).UnixMilli()
	run, err := st.CreateCodexInspectionRun(ctx, model.CodexInspectionRun{TriggerType: "manual", Status: model.CodexInspectionStatusCompleted, StartedAtMS: old, Settings: model.DefaultCodexInspectionConfig()})
	if err != nil {
		t.Fatal(err)
	}
	status := 401
	results := []model.CodexInspectionResult{{AccountKey: "delayed", FileName: "delayed.json", Provider: "codex", AuthIndex: "delayed", ObservedAtMS: old, StatusCode: &status, Action: "reauth", ErrorDetail: "invalid_token"}}
	before := time.Now().UnixMilli()
	if failures := svc.persistInspectionResults(ctx, run.ID, results, runLogger{}); failures != 0 {
		t.Fatalf("persist failures: %d", failures)
	}
	if results[0].CreatedAtMS < before {
		t.Fatal("created_at changed from insertion time")
	}
	n, ok, err := st.ClaimHealthNotification(ctx, time.Now().UnixMilli()+1)
	if err != nil || !ok {
		t.Fatalf("delayed server alert missing: %v", err)
	}
	var o model.HealthObservation
	if err := json.Unmarshal([]byte(n.Payload), &o); err != nil {
		t.Fatal(err)
	}
	if o.ObservedAtMS != old {
		t.Fatal("notification lost probe completion time")
	}
}

func TestProbeCompletionDoesNotSetInsertionTimestamp(t *testing.T) {
	svc := &Service{}
	before := time.Now().UnixMilli()
	result := svc.inspectSingleAccount(context.Background(), model.Setup{}, model.DefaultCodexInspectionConfig(), account{Provider: "codex"}, runLogger{})
	if result.ObservedAtMS < before || result.CreatedAtMS != 0 {
		t.Fatal("probe completion and insertion timestamps were conflated")
	}
}

func TestLocalReportRejectsExpiredEvidenceWithoutFrontendGuard(t *testing.T) {
	st := testutil.NewStore(t, testutil.NewConfig(t))
	st.ConfigureHealthNotifications(true, []string{"auth_invalid", "auth_recovered", "quota_exhausted"})
	svc := New(st, nil)
	ctx := context.Background()
	used := 100.0
	local := LocalHealthResult{Provider: "codex", AuthIndex: "ancient-local", ObservedAtMS: time.Now().Add(-48 * time.Hour).UnixMilli(), StatusCode: 401, FailureCode: "invalid_token", Windows: []LocalHealthWindow{{ID: "weekly", UsedPercent: &used}}}
	if _, err := svc.ReportLocalHealth(ctx, []LocalHealthResult{local}); err != nil {
		t.Fatal(err)
	}
	history, err := st.RecentHealthDeliveries(ctx)
	if err != nil || len(history) != 0 {
		t.Fatalf("expired local report queued alerts: %v", err)
	}
	key, _ := usageidentity.AccountKey(usageidentity.Fields{AuthProviderSnapshot: "codex", AuthIndex: local.AuthIndex})
	snapshots, err := st.QuotaSnapshots.ListCandidates(ctx, key, "codex", 100)
	if err != nil || len(snapshots) != 0 {
		t.Fatal("expired local report wrote quota snapshots")
	}
}

func TestLocalReportUsesLatestAccountResult(t *testing.T) {
	st := testutil.NewStore(t, testutil.NewConfig(t))
	st.ConfigureHealthNotifications(true, []string{"auth_invalid", "auth_recovered"})
	svc := New(st, nil)
	ctx := context.Background()
	failure := LocalHealthResult{Provider: "codex", AuthIndex: "backlog", ObservedAtMS: time.Now().Add(-time.Hour).UnixMilli(), StatusCode: 401, FailureCode: "invalid_token"}
	success := failure
	success.ObservedAtMS++
	success.StatusCode = 200
	success.FailureCode = ""
	if _, err := svc.ReportLocalHealth(ctx, []LocalHealthResult{success, failure}); err != nil {
		t.Fatal(err)
	}
	history, err := st.RecentHealthDeliveries(ctx)
	if err != nil || len(history) != 0 {
		t.Fatal("local historical transitions were replayed")
	}
}

func TestLocalClockToleranceAndSkippedSummary(t *testing.T) {
	st := testutil.NewStore(t, testutil.NewConfig(t))
	st.ConfigureHealthNotifications(true, []string{"auth_invalid", "auth_recovered"})
	svc := New(st, nil)
	ctx := context.Background()
	now := time.Now()
	base := LocalHealthResult{Provider: "codex", AuthIndex: "browser-ahead", ObservedAtMS: now.Add(3 * time.Minute).UnixMilli(), StatusCode: 401, FailureCode: "invalid_token"}
	duplicate := base
	duplicate.ObservedAtMS = now.Add(2 * time.Minute).UnixMilli()
	expired := base
	expired.AuthIndex = "expired"
	expired.ObservedAtMS = now.Add(-25 * time.Hour).UnixMilli()
	tooFar := base
	tooFar.ObservedAtMS = now.Add(5*time.Minute + time.Millisecond).UnixMilli()
	missing := base
	missing.AuthIndex = " "
	boundary := base
	boundary.AuthIndex = "five-minute-boundary"
	boundary.ObservedAtMS = now.Add(5 * time.Minute).UnixMilli()
	summary, err := svc.reportLocalHealthAt(ctx, []LocalHealthResult{base, duplicate, expired, tooFar, missing, boundary}, now)
	if err != nil {
		t.Fatal(err)
	}
	want := LocalHealthSkipped{Expired: 1, ClockAhead: 1, Superseded: 1, MissingIdentity: 1}
	if !summary.Accepted || summary.AcceptedCount != 2 || summary.SkippedCount != 4 || summary.SkippedReasons != want {
		t.Fatalf("incorrect summary: %+v", summary)
	}
	history, err := st.RecentHealthDeliveries(ctx)
	if err != nil || len(history) != 2 {
		t.Fatalf("modest skew discarded: %+v %v", history, err)
	}
	for i := 0; i < 2; i++ {
		n, ok, err := st.ClaimHealthNotification(ctx, time.Now().UnixMilli()+1000)
		if err != nil || !ok {
			t.Fatal("missing skewed local observation")
		}
		var observation model.HealthObservation
		if err := json.Unmarshal([]byte(n.Payload), &observation); err != nil {
			t.Fatal(err)
		}
		if observation.ObservedAtMS != now.UnixMilli() {
			t.Fatal("browser skew advanced shared state into future")
		}
		if err := st.FinishHealthNotification(ctx, n, "", false, time.Now().UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	status := 200
	svc.observeInspectionHealth(ctx, model.CodexInspectionResult{Provider: "codex", AuthIndex: base.AuthIndex, CreatedAtMS: now.UnixMilli() + 1, StatusCode: &status})
	history, err = st.RecentHealthDeliveries(ctx)
	if err != nil || len(history) != 3 || history[0].Event != "auth_recovered" {
		t.Fatalf("browser skew suppressed later server evidence: %+v %v", history, err)
	}
}

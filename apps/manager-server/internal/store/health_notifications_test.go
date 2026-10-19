package store

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
)

func TestHealthTransitionsPersistAndIsolate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "health.sqlite")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now().UnixMilli()
	o := model.HealthObservation{Provider: "codex", AuthIndex: "one", Dimension: "auth", ObservedAtMS: now}
	if err := st.ObserveHealth(ctx, o); err != nil {
		t.Fatal(err)
	}
	var count int
	st.db.QueryRow(`select count(*) from account_health_notification_state`).Scan(&count)
	if count != 0 {
		t.Fatal("disabled observer persisted state")
	}
	st.ConfigureHealthNotifications(true, []string{"auth_invalid", "auth_recovered", "quota_exhausted", "quota_recovered"})
	observe := func(o model.HealthObservation) {
		t.Helper()
		if err := st.ObserveHealth(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	observe(o) // Initial healthy baseline is silent.
	o.Unhealthy = true
	o.ObservedAtMS++
	observe(o)
	observe(o)
	// Concurrent duplicate observations enqueue exactly one transition.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := st.ObserveHealth(ctx, o); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	stale := o
	stale.Unhealthy = false
	stale.ObservedAtMS--
	observe(stale)
	other := o
	other.AuthIndex = "two"
	other.Unhealthy = false
	observe(other)
	other = o
	other.Provider = "claude"
	other.Unhealthy = false
	observe(other)
	other = o
	other.Dimension = "quota:weekly"
	observe(other)
	// Authentication recovery must not recover quota state.
	o.Unhealthy = false
	o.ObservedAtMS++
	observe(o)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.ConfigureHealthNotifications(true, []string{"auth_invalid", "auth_recovered", "quota_exhausted", "quota_recovered"})
	observe(o)
	st.db.QueryRow(`select count(*) from account_health_notification_outbox`).Scan(&count)
	if count != 3 {
		t.Fatalf("queued %d messages, want auth failure, quota failure and auth recovery", count)
	}
}

func TestHealthNotificationLeaseAndRetry(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "health.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.ConfigureHealthNotifications(true, []string{"auth_invalid", "auth_recovered", "quota_exhausted", "quota_recovered"})
	ctx := context.Background()
	now := time.Now().UnixMilli()
	if err := st.ObserveHealth(ctx, model.HealthObservation{Provider: "codex", AuthIndex: "one", Dimension: "auth", Unhealthy: true, ObservedAtMS: now}); err != nil {
		t.Fatal(err)
	}
	n, ok, err := st.ClaimHealthNotification(ctx, now+1)
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if _, ok, err := st.ClaimHealthNotification(ctx, now+2); err != nil || ok {
		t.Fatalf("lease: %v %v", ok, err)
	}
	// Crash without acknowledgement: durable lease can be reclaimed.
	n, ok, err = st.ClaimHealthNotification(ctx, now+60002)
	if err != nil || !ok || n.Attempts != 2 {
		t.Fatalf("reclaim: %+v %v %v", n, ok, err)
	}
	if err := st.FinishHealthNotification(ctx, n, "transport_error", false, now+60002); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.ClaimHealthNotification(ctx, now+60003); ok {
		t.Fatal("retry ignored backoff")
	}
	n, ok, err = st.ClaimHealthNotification(ctx, now+200003)
	if err != nil || !ok {
		t.Fatal("retry missing")
	}
	if err := st.FinishHealthNotification(ctx, n, "feishu_19021", true, now+200003); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.ClaimHealthNotification(ctx, now+999999); ok {
		t.Fatal("permanent rejection was retried")
	}
}

func TestHealthNotificationPreservesStreamOrderDuringBackoff(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "order.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.ConfigureHealthNotifications(true, []string{"auth_invalid", "auth_recovered", "quota_exhausted", "quota_recovered"})
	now := time.Now().UnixMilli()
	ctx := context.Background()
	o := model.HealthObservation{Provider: "codex", AuthIndex: "one", Dimension: "auth", Unhealthy: true, ObservedAtMS: now}
	if err := st.ObserveHealth(ctx, o); err != nil {
		t.Fatal(err)
	}
	o.Unhealthy = false
	o.ObservedAtMS++
	if err := st.ObserveHealth(ctx, o); err != nil {
		t.Fatal(err)
	}
	n, ok, err := st.ClaimHealthNotification(ctx, now+10)
	if err != nil || !ok {
		t.Fatal("first message missing")
	}
	if err := st.FinishHealthNotification(ctx, n, "transport_error", false, now+10); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.ClaimHealthNotification(ctx, now+20); ok {
		t.Fatal("recovery overtook failure in backoff")
	}
	// Other accounts remain independent while this stream is waiting.
	o.AuthIndex = "two"
	o.Unhealthy = true
	if err := st.ObserveHealth(ctx, o); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := st.ClaimHealthNotification(ctx, now+30); err != nil || !ok {
		t.Fatal("unrelated account was blocked")
	}
}

func TestHealthNotificationStopsAfterFiveTemporaryFailures(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "retries.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.ConfigureHealthNotifications(true, []string{"auth_invalid", "auth_recovered", "quota_exhausted", "quota_recovered"})
	ctx := context.Background()
	now := time.Now().UnixMilli()
	if err := st.ObserveHealth(ctx, model.HealthObservation{Provider: "codex", AuthIndex: "one", Dimension: "auth", Unhealthy: true, ObservedAtMS: now}); err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 5; attempt++ {
		now += int64(time.Hour / time.Millisecond)
		n, ok, err := st.ClaimHealthNotification(ctx, now)
		if err != nil || !ok || n.Attempts != attempt {
			t.Fatalf("attempt %d: %+v %v %v", attempt, n, ok, err)
		}
		if err := st.FinishHealthNotification(ctx, n, "transport_error", false, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok, err := st.ClaimHealthNotification(ctx, now+int64(time.Hour/time.Millisecond)); err != nil || ok {
		t.Fatalf("attempt limit: %v %v", ok, err)
	}
}

func TestDelayedEvidenceSkipsUnchangedWritesAndRejectsOutOfOrder(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "delayed.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.ConfigureHealthNotifications(true, []string{"auth_invalid", "auth_recovered"})
	ctx := context.Background()
	o := model.HealthObservation{Provider: "codex", AuthIndex: "long-run", Dimension: "auth", Unhealthy: true, ObservedAtMS: time.Now().Add(-45 * time.Minute).UnixMilli()}
	if err := st.ObserveHealth(ctx, o); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`create table health_write_audit(n integer); create trigger audit_health_update after update on account_health_notification_state begin insert into health_write_audit values(1); end`); err != nil {
		t.Fatal(err)
	}
	batch := make([]model.HealthObservation, 1000)
	for i := range batch {
		batch[i] = o
		batch[i].ObservedAtMS += int64(i + 1)
	}
	// Occupy the only SQLite connection: cached repeated states must not even
	// open a transaction or issue a query, not merely avoid UPDATE statements.
	st.db.SetMaxOpenConns(1)
	conn, err := st.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cachedCtx, cancel := context.WithTimeout(ctx, time.Second)
	err = st.ObserveHealthBatch(cachedCtx, batch)
	cancel()
	conn.Close()
	if err != nil {
		t.Fatalf("cached evidence touched SQLite: %v", err)
	}
	var writes int
	if err := st.db.QueryRow(`select count(*) from health_write_audit`).Scan(&writes); err != nil {
		t.Fatal(err)
	}
	if writes != 0 {
		t.Fatalf("unchanged evidence caused %d writes", writes)
	}
	o.Unhealthy = false
	o.ObservedAtMS += 500
	if err := st.ObserveHealth(ctx, o); err != nil {
		t.Fatal(err)
	}
	history, _ := st.RecentHealthDeliveries(ctx)
	if len(history) != 1 {
		t.Fatalf("late recovery overtook latest evidence: %+v", history)
	}
	o.ObservedAtMS += 501
	if err := st.ObserveHealth(ctx, o); err != nil {
		t.Fatal(err)
	}
	history, _ = st.RecentHealthDeliveries(ctx)
	if len(history) != 2 {
		t.Fatalf("long inspection result lost: %+v", history)
	}
}

func TestHealthBatchRollsBackAllStreams(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "atomic.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.ConfigureHealthNotifications(true, []string{"auth_invalid"})
	_, err = st.db.Exec(`create trigger reject_health_outbox before insert on account_health_notification_outbox when (select count(*) from account_health_notification_outbox)>0 begin select raise(abort,'test failure'); end`)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	batch := []model.HealthObservation{{Provider: "codex", AuthIndex: "one", Dimension: "auth", Unhealthy: true, ObservedAtMS: now}, {Provider: "codex", AuthIndex: "two", Dimension: "auth", Unhealthy: true, ObservedAtMS: now}}
	if err := st.ObserveHealthBatch(context.Background(), batch); err == nil {
		t.Fatal("expected atomic batch failure")
	}
	var count int
	if err := st.db.QueryRow(`select count(*) from account_health_notification_state`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 || len(st.healthEvidenceCache) != 0 {
		t.Fatal("failed batch leaked durable or cached state")
	}
}

func TestUnknownQuotaRearmsAcrossRestartWithoutRecovery(t *testing.T) {
	// Advance an explicit clock to test cooldown expiry; each input is fresh
	// at its simulated ingestion time, including the initial observation.
	path := filepath.Join(t.TempDir(), "unknown.sqlite")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	st.ConfigureHealthNotifications(true, []string{"quota_exhausted", "quota_recovered"})
	o := model.HealthObservation{Provider: "codex", AuthIndex: "one", Dimension: "quota:unknown:all", Window: "unknown", Unhealthy: true, ObservedAtMS: time.Now().Add(-25 * time.Hour).UnixMilli()}
	if err := st.observeHealthBatchAt(context.Background(), []model.HealthObservation{o}, time.UnixMilli(o.ObservedAtMS)); err != nil {
		t.Fatal(err)
	}
	o.ObservedAtMS += (23 * time.Hour).Milliseconds()
	if err := st.observeHealthBatchAt(context.Background(), []model.HealthObservation{o}, time.UnixMilli(o.ObservedAtMS)); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.ConfigureHealthNotifications(true, []string{"quota_exhausted", "quota_recovered"})
	o.ObservedAtMS = time.Now().UnixMilli()
	if err := st.observeHealthBatchAt(context.Background(), []model.HealthObservation{o}, time.UnixMilli(o.ObservedAtMS)); err != nil {
		t.Fatal(err)
	}
	history, err := st.RecentHealthDeliveries(context.Background())
	if err != nil || len(history) != 2 {
		t.Fatalf("missing rearmed alert: %+v %v", history, err)
	}
	for _, d := range history {
		if d.Event != "quota_exhausted" {
			t.Fatal("cooldown fabricated recovery")
		}
	}
}

func TestHealthFreshnessAndLatestBatchEvidence(t *testing.T) {
	now := time.Now()
	failure := model.HealthObservation{Provider: "codex", AuthIndex: "backlog", Dimension: "auth", Unhealthy: true, ObservedAtMS: now.Add(-time.Hour).UnixMilli()}
	success := failure
	success.Unhealthy = false
	success.ObservedAtMS++
	ancient := failure
	ancient.ObservedAtMS = now.Add(-48 * time.Hour).UnixMilli()
	future := failure
	future.ObservedAtMS = now.Add(2 * time.Minute).UnixMilli()
	for _, tc := range []struct {
		name                   string
		batch                  []model.HealthObservation
		wantAlerts, wantStates int
	}{
		{"expired first evidence", []model.HealthObservation{ancient}, 0, 0},
		{"backlog failure then success", []model.HealthObservation{failure, success}, 0, 1},
		{"unordered backlog", []model.HealthObservation{success, failure}, 0, 1},
		{"latest failure", []model.HealthObservation{success, {Provider: "codex", AuthIndex: "backlog", Dimension: "auth", Unhealthy: true, ObservedAtMS: success.ObservedAtMS + 1}}, 1, 1},
		{"future cannot mask valid failure", []model.HealthObservation{failure, future}, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, err := Open(filepath.Join(t.TempDir(), "fresh.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			st.ConfigureHealthNotifications(true, []string{"auth_invalid", "auth_recovered"})
			if err := st.observeHealthBatchAt(context.Background(), tc.batch, now); err != nil {
				t.Fatal(err)
			}
			history, err := st.RecentHealthDeliveries(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(history) != tc.wantAlerts {
				t.Fatalf("alerts=%d want %d", len(history), tc.wantAlerts)
			}
			var states int
			if err := st.db.QueryRow(`select count(*) from account_health_notification_state`).Scan(&states); err != nil {
				t.Fatal(err)
			}
			if states != tc.wantStates {
				t.Fatalf("states=%d want %d", states, tc.wantStates)
			}
		})
	}
}

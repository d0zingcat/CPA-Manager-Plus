package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
)

// Cached evidence is shared by requests and both inspection paths. The observed
// watermark advances in memory even when the durable state does not change.
// Admission is bounded; excess streams retain the durable per-observation path.
const maxHealthEvidenceCache = 16384
const unknownQuotaCooldown = 24 * time.Hour

type healthEvidence struct {
	unhealthy bool
	observed  int64
	rearmAt   int64
}

func (s *Store) ObserveHealth(ctx context.Context, o model.HealthObservation) error {
	return s.ObserveHealthBatch(ctx, []model.HealthObservation{o})
}

// ObserveHealthBatch uses one lazy transaction for all durable changes. Repeated
// cached states perform no SQL; transitions and their outbox messages are atomic.
func (s *Store) ObserveHealthBatch(ctx context.Context, observations []model.HealthObservation) error {
	return s.observeHealthBatchAt(ctx, observations, time.Now())
}

func (s *Store) observeHealthBatchAt(ctx context.Context, observations []model.HealthObservation, now time.Time) error {
	if !s.HealthNotificationsActive() {
		return nil
	}
	// A collector batch is a snapshot of the latest available evidence, not a
	// replay of every historical transition. Normalize and reject invalid times
	// before choosing each stream's newest result. On equal times prefer failure.
	latest := make([]model.HealthObservation, 0, len(observations))
	indices := make(map[string]int)
	for _, o := range observations {
		o.Provider = strings.ToLower(strings.TrimSpace(o.Provider))
		o.AuthIndex = strings.TrimSpace(o.AuthIndex)
		if o.Provider == "" || o.AuthIndex == "" || o.Dimension == "" || !model.HealthObservationFresh(o.ObservedAtMS, now) {
			continue
		}
		key := o.Provider + "\x00" + o.AuthIndex + "\x00" + o.Dimension
		if index, ok := indices[key]; ok {
			prior := latest[index]
			if o.ObservedAtMS > prior.ObservedAtMS || (o.ObservedAtMS == prior.ObservedAtMS && o.Unhealthy) {
				latest[index] = o
			}
		} else {
			indices[key] = len(latest)
			latest = append(latest, o)
		}
	}
	if len(latest) == 0 {
		return nil
	}
	s.healthNotificationsMu.Lock()
	defer s.healthNotificationsMu.Unlock()
	var tx *sql.Tx
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()
	begin := func() error {
		if tx != nil {
			return nil
		}
		var err error
		tx, err = s.db.BeginTx(ctx, nil)
		return err
	}
	staged := make(map[string]healthEvidence)
	for _, o := range latest {
		hash := sha256.Sum256([]byte(o.Provider + "\x00" + o.AuthIndex))
		key := hex.EncodeToString(hash[:])
		stream := key + ":" + o.Dimension
		old, exists := staged[stream]
		if !exists {
			old, exists = s.healthEvidenceCache[stream]
		}
		unknown := strings.HasPrefix(o.Dimension, "quota:unknown:")
		if !exists {
			if err := begin(); err != nil {
				return err
			}
			err := tx.QueryRowContext(ctx, `select unhealthy,observed_at_ms,rearm_at_ms from account_health_notification_state where credential_key=? and dimension=?`, key, o.Dimension).Scan(&old.unhealthy, &old.observed, &old.rearmAt)
			exists = err == nil
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if unknown && exists && old.rearmAt == 0 {
				old.rearmAt = old.observed + unknownQuotaCooldown.Milliseconds()
			}
		}
		if exists && o.ObservedAtMS <= old.observed {
			continue
		}
		rearmed := exists && unknown && o.Unhealthy && o.ObservedAtMS >= old.rearmAt
		changed := !exists || old.unhealthy != o.Unhealthy || rearmed
		next := old
		next.unhealthy, next.observed = o.Unhealthy, o.ObservedAtMS
		if unknown && changed {
			next.rearmAt = o.ObservedAtMS + unknownQuotaCooldown.Milliseconds()
		}
		// Once the cache is full, preserve ordering durably for unadmitted streams.
		_, cached := s.healthEvidenceCache[stream]
		_, stagedStream := staged[stream]
		cacheable := cached || stagedStream || len(s.healthEvidenceCache)+len(staged) < maxHealthEvidenceCache
		if changed || !cacheable {
			if err := begin(); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `insert into account_health_notification_state(credential_key,dimension,unhealthy,observed_at_ms,rearm_at_ms) values(?,?,?,?,?) on conflict(credential_key,dimension) do update set unhealthy=excluded.unhealthy,observed_at_ms=excluded.observed_at_ms,rearm_at_ms=excluded.rearm_at_ms`, key, o.Dimension, o.Unhealthy, o.ObservedAtMS, next.rearmAt)
			if err != nil {
				return err
			}
		}
		if ((exists && old.unhealthy != o.Unhealthy) || (!exists && o.Unhealthy) || rearmed) && s.HealthEventEnabled(model.HealthEvent(o)) {
			payload, err := json.Marshal(o)
			if err != nil {
				return err
			}
			createdAtMS := now.UnixMilli()
			if _, err := tx.ExecContext(ctx, `insert into account_health_notification_outbox(stream_key,payload,created_at_ms,next_attempt_ms) values(?,?,?,?)`, stream, string(payload), createdAtMS, createdAtMS); err != nil {
				return err
			}
		}
		if cacheable {
			staged[stream] = next
		}
	}
	if tx != nil {
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	if s.healthEvidenceCache == nil {
		s.healthEvidenceCache = make(map[string]healthEvidence)
	}
	for key, state := range staged {
		s.healthEvidenceCache[key] = state
	}
	return nil
}

// ClaimHealthNotification is atomic across workers. A process crash releases the
// lease after one minute; Feishu has no idempotency key, so delivery is at least once.
func (s *Store) ClaimHealthNotification(ctx context.Context, nowMS int64) (model.HealthNotification, bool, error) {
	var n model.HealthNotification
	err := s.db.QueryRowContext(ctx, `update account_health_notification_outbox set lease_until_ms=?,attempts=attempts+1 where id=(select q.id from account_health_notification_outbox q where q.status='pending' and q.next_attempt_ms<=? and q.lease_until_ms<=? and not exists(select 1 from account_health_notification_outbox earlier where earlier.stream_key=q.stream_key and earlier.status='pending' and earlier.id<q.id) order by q.id limit 1) returning id,payload,attempts`, nowMS+60000, nowMS, nowMS).Scan(&n.ID, &n.Payload, &n.Attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return n, false, nil
	}
	return n, err == nil, err
}

func (s *Store) FinishHealthNotification(ctx context.Context, n model.HealthNotification, reason string, permanent bool, nowMS int64) error {
	status := "pending"
	if reason == "" {
		status = "sent"
	} else if permanent || n.Attempts >= 5 {
		status = "failed"
		if reason == "event_disabled" {
			status = "skipped"
		}
	}
	delay := time.Duration(1<<min(n.Attempts, 5)) * 30 * time.Second
	_, err := s.db.ExecContext(ctx, `update account_health_notification_outbox set status=?,last_error=?,next_attempt_ms=?,lease_until_ms=0 where id=? and attempts=?`, status, reason, nowMS+delay.Milliseconds(), n.ID, n.Attempts)
	return err
}

func (s *Store) PruneHealthNotifications(ctx context.Context, nowMS int64) error {
	// Keep a bounded time horizon for terminal deliveries; unresolved states remain
	// so a restart cannot re-notify an unchanged broken account.
	_, err := s.db.ExecContext(ctx, `delete from account_health_notification_outbox where status!='pending' and created_at_ms<?`, nowMS-(30*24*time.Hour).Milliseconds())
	return err
}

func (s *Store) ConfigureHealthNotifications(enabled bool, events []string) {
	s.healthNotificationPolicy.Store(&model.HealthNotificationPolicy{Enabled: enabled, Events: append([]string{}, events...)})
}
func (s *Store) HealthNotificationsActive() bool {
	if p := s.healthNotificationPolicy.Load(); p != nil {
		return p.Enabled
	}
	return false
}
func (s *Store) HealthEventEnabled(event string) bool {
	if p := s.healthNotificationPolicy.Load(); p != nil {
		if !p.Enabled {
			return false
		}
		for _, e := range p.Events {
			if e == event {
				return true
			}
		}
		return false
	}
	return false
}

type HealthDelivery struct {
	ID          int64  `json:"id"`
	Event       string `json:"event"`
	Status      string `json:"status"`
	Attempts    int    `json:"attempts"`
	Error       string `json:"error,omitempty"`
	CreatedAtMS int64  `json:"createdAtMs"`
}

func (s *Store) RecentHealthDeliveries(ctx context.Context) ([]HealthDelivery, error) {
	rows, err := s.db.QueryContext(ctx, `select id,payload,status,attempts,last_error,created_at_ms from account_health_notification_outbox order by id desc limit 20`)
	if err != nil {
		return nil, errors.New("cannot load notification history")
	}
	defer rows.Close()
	result := []HealthDelivery{}
	for rows.Next() {
		var d HealthDelivery
		var raw string
		if err := rows.Scan(&d.ID, &raw, &d.Status, &d.Attempts, &d.Error, &d.CreatedAtMS); err != nil {
			return nil, errors.New("cannot read notification history")
		}
		var o model.HealthObservation
		if json.Unmarshal([]byte(raw), &o) == nil {
			d.Event = model.HealthEvent(o)
		}
		result = append(result, d)
	}
	return result, rows.Err()
}

func (s *Store) RecordHealthTest(ctx context.Context, reason string) error {
	status := "sent"
	if reason != "" {
		status = "failed"
	}
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx, `insert into account_health_notification_outbox(stream_key,payload,created_at_ms,next_attempt_ms,status,attempts,last_error) values('test','{"dimension":"test"}',?,?,?,1,?)`, now, now, status, reason)
	if err != nil {
		return errors.New("cannot save test notification result")
	}
	return nil
}

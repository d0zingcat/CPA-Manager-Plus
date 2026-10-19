package worker

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/codexquota"
	collectorpkg "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	quotasnapshotrepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/quotasnapshot"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usage"
)

// HealthNotificationWorker observes live collector events independently of
// auto-disable settings. Quota evidence goes through the shared snapshot writer.
type HealthNotificationWorker struct{ store *store.Store }

func NewHealthNotificationWorker(st *store.Store) *HealthNotificationWorker {
	return &HealthNotificationWorker{store: st}
}
func (w *HealthNotificationWorker) HandleUsageEvents(ctx context.Context, _ collectorpkg.RuntimeConfig, events []usage.Event) {
	if !w.store.HealthNotificationsActive() {
		return
	}
	observations := make([]model.HealthObservation, 0, len(events))
	for _, event := range events {
		if o, ok := w.quotaFailureObservation(event); ok {
			observations = append(observations, o)
		}
		unhealthy := false
		reasonCode := ""
		if event.Failed {
			decision, ok := classifyAccountActionEvent(event)
			if !ok || decision.Action != credentialpolicy.ActionReauth {
				continue
			}
			unhealthy = true
			reasonCode = decision.ReasonCode
		}
		o := model.HealthObservation{Provider: credentialpolicy.NormalizeProvider(firstNonEmpty(event.AuthProviderSnapshot, event.Provider)), AuthIndex: event.AuthIndex, Email: model.HealthAccountEmail(event.AccountSnapshot), Source: "usage", StatusCode: event.FailStatusCode, ReasonCode: reasonCode, Dimension: "auth", Unhealthy: unhealthy, ObservedAtMS: event.TimestampMS}
		observations = append(observations, o)
	}
	if err := w.store.ObserveHealthBatch(ctx, observations); err != nil {
		log.Print("[feishu] usage observation persistence failed")
	}
}

// Header-based quota percentages are handled by the common snapshot writer.
// Explicit structured quota errors still notify when no percentage is present.
func (w *HealthNotificationWorker) quotaFailureObservation(event usage.Event) (model.HealthObservation, bool) {
	if !event.Failed {
		return model.HealthObservation{}, false
	}
	provider := credentialpolicy.NormalizeProvider(firstNonEmpty(event.AuthProviderSnapshot, event.Provider))
	window := ""
	scopeKind := "all"
	scopeKey := ""
	var models []string
	resetAtMS := int64(0)
	candidate, known := quotaAutoDisableCandidateFromEvent(event, "", "", time.Now())
	switch provider {
	case "codex":
		metadata := event.ResponseMetadata
		if metadata == nil && event.ResponseMetadataJSON != "" {
			metadata = usage.ResponseHeaderMetadataFromJSON(event.ResponseMetadataJSON)
		}
		// The snapshot writer already handles full header windows with their exact
		// canonical scope and ID. Do not manufacture a second, less specific event.
		if metadata != nil && metadata.Quota != nil && codexQuotaHasFullWindow(metadata.Quota) {
			return model.HealthObservation{}, false
		}
		code, typ := accountActionErrorCodeAndType(event)
		explicit := event.FailStatusCode == 429 && (strings.EqualFold(code, "usage_limit_reached") || strings.EqualFold(typ, "usage_limit_reached"))
		if !known && !explicit {
			return model.HealthObservation{}, false
		}
		kind := codexQuotaWindowKindFromEvent(event)
		window = "unknown"
		switch kind {
		case "five_hour":
			window = "five-hour"
		case "weekly":
			window = "weekly"
		case "monthly":
			window = "monthly"
		}
		quotaScope := codexquota.ResolveUsageScope(event.Model, event.AnalyticsModel, event.RequestedModel, event.ResolvedModel)
		scope := quotaScope.Scope
		scopeKind, scopeKey, models = scope.Kind, scope.Key, scope.Models
		if quotaScope.ProviderWindowPrefix != "" && window != "unknown" {
			window = quotaScope.ProviderWindowPrefix + "-" + window + "-0"
		}
		if known {
			resetAtMS = candidate.ResetAt.UnixMilli()
		}
	case "xai":
		if !known {
			return model.HealthObservation{}, false
		}
		window = "included-free-rolling-24h"
		if metadata := xaiProviderUsageFromEvent(event, time.Now()); metadata != nil && metadata.Model != "" {
			scopeKind = "models"
			scopeKey = strings.ToLower(metadata.Model)
			models = []string{metadata.Model}
		}
		if xaiFreeUsageHasExplicitReset(event, time.Now()) {
			resetAtMS = candidate.ResetAt.UnixMilli()
		}
	default:
		return model.HealthObservation{}, false
	}
	o := model.HealthObservation{Provider: provider, AuthIndex: event.AuthIndex, Email: model.HealthAccountEmail(event.AccountSnapshot), Source: "usage", StatusCode: event.FailStatusCode, Dimension: "quota:" + window + ":" + quotasnapshotrepo.ScopeFingerprint(scopeKind, scopeKey, models), Window: window, Unhealthy: true, ObservedAtMS: event.TimestampMS, ResetAtMS: resetAtMS}
	return o, true
}

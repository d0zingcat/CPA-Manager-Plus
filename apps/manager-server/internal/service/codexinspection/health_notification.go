package codexinspection

import (
	"context"
	"log"
	"math"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	quotasnapshotrepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/quotasnapshot"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
)

func (s *Service) observeInspectionHealth(ctx context.Context, result model.CodexInspectionResult) {
	if !s.store.HealthNotificationsActive() {
		return
	}
	observe := func(o model.HealthObservation) {
		o.Email = model.HealthAccountEmail(result.AccountSnapshot)
		o.Source = "inspection"
		if result.StatusCode != nil {
			o.StatusCode = *result.StatusCode
		}
		if err := s.store.ObserveHealth(ctx, o); err != nil {
			log.Print("[feishu] inspection health observation persistence failed")
		}
	}
	authenticated := false
	if result.StatusCode != nil && *result.StatusCode >= 200 && *result.StatusCode < 300 {
		switch result.ErrorKind {
		case "", "billing_healthy", "official_api_healthy", "inference_healthy":
			authenticated = true
		}
	}
	needsReauth := false
	reasonCode := ""
	// Older inspection decisions suggest re-login for any 401. Notification
	// requires the shared classifier's explicit credential semantics instead.
	if result.Action == "reauth" && result.StatusCode != nil {
		decision, ok := credentialpolicy.EvaluateFailure(credentialpolicy.FailureSignal{Provider: result.Provider, StatusCode: *result.StatusCode, Summary: result.ErrorDetail})
		needsReauth = ok && decision.Action == credentialpolicy.ActionReauth
		if needsReauth {
			reasonCode = decision.ReasonCode
		}
	}
	if needsReauth || authenticated {
		observe(model.HealthObservation{Provider: result.Provider, AuthIndex: result.AuthIndex, Dimension: "auth", ReasonCode: reasonCode, Unhealthy: needsReauth, ObservedAtMS: result.CreatedAtMS})
	}
	// Codex windows are handled by the shared snapshot writer. xAI's stable
	// billing windows are available directly on persisted inspection results.
	// Indexed product rows have no stable scope identity and are ignored.
	if result.Provider != "xai" {
		return
	}
	for _, window := range result.QuotaWindows {
		switch window.ID {
		case "xai-weekly", "xai-monthly", "xai-on-demand":
		default:
			continue
		}
		if window.UsedPercent == nil || math.IsNaN(*window.UsedPercent) || math.IsInf(*window.UsedPercent, 0) || *window.UsedPercent < 0 || *window.UsedPercent > 100 {
			continue
		}
		if window.ResetAtMS > 0 && window.ResetAtMS <= result.CreatedAtMS {
			continue
		}
		observe(model.HealthObservation{Provider: result.Provider, AuthIndex: result.AuthIndex, Dimension: "quota:" + window.ID + ":" + quotasnapshotrepo.ScopeFingerprint("all", "", nil), Window: window.ID, Unhealthy: *window.UsedPercent >= 100, ObservedAtMS: result.CreatedAtMS, ResetAtMS: window.ResetAtMS})
	}
}

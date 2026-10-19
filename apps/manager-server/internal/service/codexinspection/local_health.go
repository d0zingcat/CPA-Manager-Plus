package codexinspection

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
)

type LocalHealthWindow struct {
	ID                 string                                `json:"id"`
	UsedPercent        *float64                              `json:"usedPercent"`
	ResetAtMS          int64                                 `json:"resetAtMs"`
	ResetAccuracy      string                                `json:"resetAccuracy"`
	LimitWindowSeconds *float64                              `json:"limitWindowSeconds"`
	ModelScope         *model.CodexInspectionQuotaModelScope `json:"modelScope"`
}
type LocalHealthResult struct {
	Provider     string              `json:"provider"`
	AuthIndex    string              `json:"authIndex"`
	ObservedAtMS int64               `json:"observedAtMs"`
	StatusCode   int                 `json:"statusCode"`
	ErrorKind    string              `json:"errorKind"`
	FailureCode  string              `json:"failureCode"`
	Windows      []LocalHealthWindow `json:"windows"`
}

type LocalHealthSkipped struct {
	Expired         int `json:"expired"`
	ClockAhead      int `json:"clockAhead"`
	Superseded      int `json:"superseded"`
	MissingIdentity int `json:"missingIdentity"`
}

type LocalHealthReportSummary struct {
	Accepted       bool               `json:"accepted"`
	AcceptedCount  int                `json:"acceptedCount"`
	SkippedCount   int                `json:"skippedCount"`
	SkippedReasons LocalHealthSkipped `json:"skippedReasons"`
}

func (s *Service) ReportLocalHealth(ctx context.Context, results []LocalHealthResult) (LocalHealthReportSummary, error) {
	return s.reportLocalHealthAt(ctx, results, time.Now())
}

func (s *Service) reportLocalHealthAt(ctx context.Context, results []LocalHealthResult, now time.Time) (LocalHealthReportSummary, error) {
	summary := LocalHealthReportSummary{Accepted: true}

	if len(results) > 200 {
		return summary, errors.New("too many local inspection results")
	}
	for _, r := range results {
		if r.ObservedAtMS <= 0 || r.StatusCode < 0 || r.StatusCode > 599 {
			return summary, errors.New("invalid local observation timestamp or status")
		}
		if (r.Provider != "codex" && r.Provider != "xai") || len(r.AuthIndex) > 256 || len(r.Windows) > 16 {
			return summary, errors.New("invalid local inspection identity or windows")
		}
		if r.FailureCode != "" && r.StatusCode != 401 && r.StatusCode != 403 {
			return summary, errors.New("credential failure requires authentication error status")
		}
		if r.FailureCode != "" && r.FailureCode != "invalid_token" && r.FailureCode != "token_revoked" {
			return summary, errors.New("invalid credential failure code")
		}
		switch r.ErrorKind {
		case "", "billing_healthy", "official_api_healthy", "inference_healthy", "unknown":
		default:
			return summary, errors.New("invalid local health classification")
		}
		for _, w := range r.Windows {
			if len(w.ID) > 200 || w.ID == "" || (w.UsedPercent != nil && (math.IsNaN(*w.UsedPercent) || math.IsInf(*w.UsedPercent, 0) || *w.UsedPercent < 0 || *w.UsedPercent > 100)) {
				return summary, errors.New("invalid local quota window")
			}
		}
	}
	latest := make([]LocalHealthResult, 0, len(results))
	indices := make(map[string]int)
	for _, r := range results {
		switch {
		case r.ObservedAtMS < now.Add(-24*time.Hour).UnixMilli():
			summary.SkippedReasons.Expired++
			summary.SkippedCount++
			continue
		case r.ObservedAtMS > now.Add(5*time.Minute).UnixMilli():
			summary.SkippedReasons.ClockAhead++
			summary.SkippedCount++
			continue
		case strings.TrimSpace(r.AuthIndex) == "":
			summary.SkippedReasons.MissingIdentity++
			summary.SkippedCount++
			continue
		}
		key := r.Provider + "\x00" + strings.TrimSpace(r.AuthIndex)
		if index, ok := indices[key]; ok {
			summary.SkippedReasons.Superseded++
			summary.SkippedCount++
			prior := latest[index]
			if r.ObservedAtMS > prior.ObservedAtMS || (r.ObservedAtMS == prior.ObservedAtMS && r.FailureCode != "") {
				latest[index] = r
			}
		} else {
			indices[key] = len(latest)
			latest = append(latest, r)
		}
	}
	for _, r := range latest {
		// Accept modest browser skew, but never advance shared health/quotas into
		// the future. Keep the original time solely for a stable local source ID.
		observedAtMS := min(r.ObservedAtMS, now.UnixMilli())
		result := model.CodexInspectionResult{Provider: r.Provider, AuthIndex: r.AuthIndex, CreatedAtMS: observedAtMS, ObservedAtMS: r.ObservedAtMS, StatusCode: &r.StatusCode, ErrorKind: r.ErrorKind, ErrorDetail: r.FailureCode}
		if r.FailureCode != "" {
			result.Action = "reauth"
		}
		for _, w := range r.Windows {
			result.QuotaWindows = append(result.QuotaWindows, model.CodexInspectionQuotaWindow{ID: w.ID, UsedPercent: w.UsedPercent, ResetAtMS: w.ResetAtMS, ResetAccuracy: w.ResetAccuracy, LimitWindowSeconds: w.LimitWindowSeconds, ModelScope: w.ModelScope})
		}
		s.observeInspectionHealth(ctx, result)
		if err := s.quotaSnapshots.WriteCodexInspectionResult(ctx, result); err != nil {
			return summary, errors.New("cannot record local quota observation")
		}
		summary.AcceptedCount++
	}
	return summary, nil
}

import type { LocalHealthReportSummary } from '@/services/api/notifications';
import type { CodexInspectionRunResult } from '@/features/monitoring/codexInspection';

// Send only classified evidence and normalized quota metadata. Upstream response
// bodies, error summaries, account emails and tokens never leave the browser.
export function buildLocalHealthReport(run: CodexInspectionRunResult) {
  return run.results
    .filter((item) => (item.provider === 'codex' || item.provider === 'xai') && item.authIndex)
    .map((item) => {
      const detail = `${item.errorKind || ''}\n${item.errorDetail || ''}`.toLowerCase();
      let failureCode = '';
      if (item.statusCode === 401 || item.statusCode === 403) {
        if (
          /token_revoked|token_invalidated|invalidated_oauth_token|oauth token revoked|token has been invalidated/.test(
            detail
          )
        )
          failureCode = 'token_revoked';
        else if (
          /invalid_token|invalid_grant|invalid or expired credentials|authentication token is expired|token is expired|no auth context|requires reauthorization|requires re-authentication/.test(
            detail
          )
        )
          failureCode = 'invalid_token';
      }
      const healthyKinds = ['', 'billing_healthy', 'official_api_healthy', 'inference_healthy'];
      const errorKind = healthyKinds.includes(item.errorKind || '')
        ? item.errorKind || ''
        : 'unknown';
      return {
        provider: item.provider,
        authIndex: item.authIndex!,
        observedAtMs: item.observedAtMs || run.finishedAt,
        statusCode: item.statusCode || 0,
        errorKind,
        failureCode,
        windows: (item.quotaWindows || [])
          .slice(0, 16)
          .filter(
            (window) =>
              window.usedPercent != null &&
              Number.isFinite(window.usedPercent) &&
              window.usedPercent >= 0 &&
              window.usedPercent <= 100
          )
          .map((window) => ({
            id: window.id,
            usedPercent: window.usedPercent,
            resetAtMs: window.resetAtMs || 0,
            resetAccuracy: window.resetAccuracy || 'unknown',
            limitWindowSeconds: window.limitWindowSeconds ?? null,
            modelScope: window.modelScope || null,
          })),
      };
    });
}

// Older Manager versions only return { accepted: true }.
export function localHealthSkippedSummary(reports: LocalHealthReportSummary[]) {
  const total = { count: 0, expired: 0, clockAhead: 0, superseded: 0, missingIdentity: 0 };
  for (const report of reports) {
    total.count += report.skippedCount || 0;
    total.expired += report.skippedReasons?.expired || 0;
    total.clockAhead += report.skippedReasons?.clockAhead || 0;
    total.superseded += report.skippedReasons?.superseded || 0;
    total.missingIdentity += report.skippedReasons?.missingIdentity || 0;
  }
  return total;
}

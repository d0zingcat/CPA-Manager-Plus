import { describe, expect, it } from 'vitest';
import type { CodexInspectionRunResult } from '@/features/monitoring/codexInspection';
import { buildLocalHealthReport, localHealthSkippedSummary } from './localHealthReport';
const run = (statusCode: number, errorDetail: string) =>
  ({
    startedAt: 1000,
    finishedAt: 2000,
    results: [
      {
        provider: 'codex',
        authIndex: 'one',
        statusCode,
        errorDetail,
        errorKind: '',
        accountSnapshot: 'private@example.test',
        raw: { token: 'sk-private' },
        quotaWindows: [{ id: 'weekly', usedPercent: 100 }],
      },
    ],
  }) as unknown as CodexInspectionRunResult;
describe('local inspection notification reports', () => {
  it('normalizes explicit credential errors without exposing raw responses or account data', () => {
    const report = buildLocalHealthReport(
      run(401, 'invalid_token Bearer sk-private private@example.test')
    );
    expect(report[0].failureCode).toBe('invalid_token');
    expect(report[0].observedAtMs).toBe(2000);
    expect(JSON.stringify(report)).not.toMatch(/sk-private|Bearer|private@example/);
    expect(report[0].windows[0].usedPercent).toBe(100);
  });
  it('preserves each account completion time after a run longer than fifteen minutes', () => {
    const input = run(401, 'invalid_token');
    input.finishedAt = 40 * 60 * 1000;
    input.results[0].observedAtMs = 2000;
    input.results.push({ ...input.results[0], authIndex: 'two', observedAtMs: 30 * 60 * 1000 });
    expect(buildLocalHealthReport(input).map((item) => item.observedAtMs)).toEqual([
      2000,
      30 * 60 * 1000,
    ]);
  });
  it('does not infer re-login from ordinary 401/403, rate limiting or transport errors', () => {
    for (const [status, detail] of [
      [401, 'unauthorized'],
      [403, 'region restricted'],
      [429, 'rate limited'],
      [500, 'invalid_token'],
      [0, 'network error'],
    ] as const) {
      expect(buildLocalHealthReport(run(status, detail))[0].failureCode).toBe('');
    }
  });
  it('omits invalid and missing quota percentages', () => {
    const input = run(200, '');
    input.results[0].quotaWindows = [
      { id: 'one', usedPercent: null },
      { id: 'two', usedPercent: Number.NaN },
      { id: 'three', usedPercent: -1 },
    ] as (typeof input.results)[0]['quotaWindows'];
    expect(buildLocalHealthReport(input)[0].windows).toEqual([]);
  });
});

describe('local inspection report summaries', () => {
  it('totals skipped results and reasons across chunks', () => {
    expect(
      localHealthSkippedSummary([
        {
          accepted: true,
          acceptedCount: 98,
          skippedCount: 2,
          skippedReasons: { expired: 1, clockAhead: 1, superseded: 0, missingIdentity: 0 },
        },
        {
          accepted: true,
          acceptedCount: 98,
          skippedCount: 2,
          skippedReasons: { expired: 0, clockAhead: 0, superseded: 1, missingIdentity: 1 },
        },
      ])
    ).toEqual({ count: 4, expired: 1, clockAhead: 1, superseded: 1, missingIdentity: 1 });
  });
  it('accepts the response from older Manager versions without warnings', () => {
    expect(localHealthSkippedSummary([{ accepted: true }]).count).toBe(0);
  });
});

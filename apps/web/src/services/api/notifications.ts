import { apiClient } from './client';
import { normalizeApiBase } from '@/utils/connection';
export const notificationEvents = [
  'auth_invalid',
  'auth_recovered',
  'quota_exhausted',
  'quota_recovered',
] as const;
export type NotificationEvent = (typeof notificationEvents)[number];
export interface NotificationDelivery {
  id: number;
  event: string;
  status: string;
  attempts: number;
  error?: string;
  createdAtMs: number;
}
export interface FeishuStatus {
  configurationError?: string;
  enabled: boolean;
  locked: boolean;
  source: string;
  webhookConfigured: boolean;
  signingSecretConfigured: boolean;
  events: NotificationEvent[];
  updatedAtMs: number;
  deliveries: NotificationDelivery[];
}
export interface FeishuUpdate {
  replaceConfiguration?: boolean;
  enabled: boolean;
  webhookUrl?: string;
  signingSecret?: string;
  clearSigningSecret?: boolean;
  clearWebhook?: boolean;
  events: NotificationEvent[];
}
export interface LocalHealthReportSummary {
  accepted: boolean;
  acceptedCount?: number;
  skippedCount?: number;
  skippedReasons?: {
    expired: number;
    clockAhead: number;
    superseded: number;
    missingIdentity: number;
  };
}
const endpoint = (base: string, suffix = '') =>
  `${normalizeApiBase(base)}/usage-service/notifications/feishu${suffix}`;
export const notificationsApi = {
  status: (base: string) => apiClient.get<FeishuStatus>(endpoint(base)),
  update: (base: string, settings: FeishuUpdate) =>
    apiClient.patch<FeishuStatus>(endpoint(base), settings),
  test: (base: string) => apiClient.post<{ sent: boolean }>(endpoint(base, '/test')),
  reportLocal: (base: string, results: unknown[]) =>
    apiClient.post<LocalHealthReportSummary>(
      `${normalizeApiBase(base)}/usage-service/notifications/local-inspection`,
      {
        results,
      }
    ),
};

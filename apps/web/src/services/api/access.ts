import { apiClient } from './client';
import { normalizeApiBase } from '@/utils/connection';

export type AccessRole = 'admin' | 'usage_viewer';
export type ReadonlyKeyStatus = { enabled: boolean; updatedAtMs: number };
const endpoint = (base: string, path: string) => `${normalizeApiBase(base)}/usage-service/${path}`;
export const accessApi = {
  session: (base: string) =>
    apiClient.get<{ role: AccessRole; capabilities: string[] }>(endpoint(base, 'auth/session')),
  keyStatus: (base: string) => apiClient.get<ReadonlyKeyStatus>(endpoint(base, 'readonly-key')),
  rotateKey: (base: string) => apiClient.post<{ key: string }>(endpoint(base, 'readonly-key')),
  disableKey: (base: string) => apiClient.delete(endpoint(base, 'readonly-key')),
};

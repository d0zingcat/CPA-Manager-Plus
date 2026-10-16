// Role is accepted from the session endpoint, never inferred from stored state.
let readonlyCredential = '';
export function setReadonlyCredential(key: string, readOnly: boolean) {
  readonlyCredential = readOnly ? key : '';
}
export function assertReadonlyRequest(method: string, url: string, authorization: unknown) {
  if (!readonlyCredential || authorization !== `Bearer ${readonlyCredential}`) return;
  const path = new URL(url, 'http://local').pathname;
  const queryPaths = [
    '/v0/management/monitoring/analytics',
    '/v0/management/quota-snapshots/query',
    '/v0/management/monitoring/account-history',
    '/v0/management/monitoring/account-window-usage',
    '/monitoring/analytics',
    '/quota-snapshots/query',
    '/monitoring/account-history',
    '/monitoring/account-window-usage',
  ];
  if (
    method.toLowerCase() === 'get' &&
    !/(?:-auth-url|auth-files\/download|config\/export|usage\/export|request-log-by-id|request-error-logs\/)/.test(
      path
    )
  )
    return;
  if (method.toLowerCase() === 'post' && queryPaths.includes(path)) return;
  throw new Error('Read-only access: changes and credential downloads are disabled.');
}

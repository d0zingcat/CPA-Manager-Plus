import { afterEach, describe, expect, it } from 'vitest';
import { assertReadonlyRequest, setReadonlyCredential } from './readonlyAccess';
afterEach(() => setReadonlyCredential('', false));
describe('readonly request guard', () => {
  it('allows existing query APIs while blocking writes and credential downloads', () => {
    setReadonlyCredential('viewer', true);
    const auth = 'Bearer viewer';
    expect(() => assertReadonlyRequest('GET', '/config', auth)).not.toThrow();
    for (const path of [
      '/monitoring/analytics',
      '/v0/management/quota-snapshots/query',
      '/monitoring/account-history',
    ])
      expect(() => assertReadonlyRequest('POST', path, auth)).not.toThrow();
    for (const [method, path] of [
      ['PUT', '/config'],
      ['POST', '/usage/import'],
      ['GET', '/codex-auth-url'],
      ['GET', '/auth-files/download'],
      ['DELETE', '/auth-files'],
      ['POST', '/api-call'],
    ])
      expect(() => assertReadonlyRequest(method, path, auth)).toThrow('Read-only');
  });
  it('does not interfere with administrator or independently scoped credentials', () => {
    setReadonlyCredential('viewer', true);
    expect(() => assertReadonlyRequest('PUT', '/config', 'Bearer admin')).not.toThrow();
    setReadonlyCredential('admin', false);
    expect(() => assertReadonlyRequest('PUT', '/config', 'Bearer admin')).not.toThrow();
  });
});

import type { ReactNode } from 'react';
import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { describe, expect, it, vi } from 'vitest';
import { RootShell } from './RootShell';
const mocks = vi.hoisted(() => ({
  auth: { isAuthenticated: true, role: 'usage_viewer' },
  updates: vi.fn(),
}));
vi.mock('@/stores/useAuthStore', () => ({
  useAuthStore: (selector: (state: unknown) => unknown) => selector(mocks.auth),
}));
vi.mock('react-router-dom', () => ({ Outlet: () => <div>outlet</div> }));
vi.mock('@/components/common/AppBackground', () => ({ AppBackground: () => null }));
vi.mock('@/components/common/ConfirmationModal', () => ({ ConfirmationModal: () => null }));
vi.mock('@/components/common/NotificationContainer', () => ({ NotificationContainer: () => null }));
vi.mock('@/features/system/ManagerUpdates', () => ({
  ManagerUpdates: ({ children }: { children: ReactNode }) => {
    mocks.updates();
    return children;
  },
}));
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
describe('RootShell authenticated layout', () => {
  it('does not mount authenticated discovery for unverified sessions', async () => {
    for (const auth of [{ isAuthenticated: false, role: 'admin' }]) {
      mocks.auth = auth;
      mocks.updates.mockClear();
      let renderer: ReactTestRenderer;
      await act(async () => {
        renderer = create(<RootShell />);
      });
      expect(mocks.updates).not.toHaveBeenCalled();
      await act(async () => renderer!.unmount());
    }
  });
  it('mounts the shared update provider for authenticated viewers', async () => {
    mocks.auth = { isAuthenticated: true, role: 'usage_viewer' };
    mocks.updates.mockClear();
    let renderer: ReactTestRenderer;
    await act(async () => {
      renderer = create(<RootShell />);
    });
    expect(mocks.updates).toHaveBeenCalledOnce();
    await act(async () => renderer!.unmount());
  });
});

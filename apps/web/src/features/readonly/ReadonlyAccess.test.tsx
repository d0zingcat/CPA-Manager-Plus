import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { Button } from '@/components/ui/Button';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import { AccessLayout } from './AccessLayout';
import { ReadonlyKeyCard } from './ReadonlyKeyCard';
import { ReadOnlyButton } from './ReadOnlyContext';
import { ReadOnlyContext } from './readOnlyState';
const mocks = vi.hoisted(() => ({
  auth: { role: 'usage_viewer', managementKey: 'viewer' },
  keyStatus: vi.fn().mockResolvedValue({ enabled: true, updatedAtMs: 1 }),
  rotateKey: vi.fn().mockResolvedValue({ key: 'local-test-key' }),
  disableKey: vi.fn(),
}));
vi.mock('@/stores/useAuthStore', () => ({
  useAuthStore: (selector: (s: unknown) => unknown) => selector(mocks.auth),
}));
vi.mock('@/services/api/access', () => ({ accessApi: mocks }));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (k: string) => k }) }));
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let renderer: ReactTestRenderer;
afterEach(async () => {
  if (renderer) await act(async () => renderer.unmount());
});
describe('shared read-only management interface', () => {
  it('renders the exact same page and navigation, preserving read interactions', async () => {
    const page = (
      <main>
        <a href="/config">configuration</a>
        <a href="/usage-analytics">usage</a>
        <Button>filter</Button>
        <Button writeAction>save</Button>
        <Button variant="danger">delete</Button>
        <ReadOnlyButton>import</ReadOnlyButton>
      </main>
    );
    mocks.auth.role = 'usage_viewer';
    await act(async () => {
      renderer = create(<AccessLayout admin={page} />);
    });
    expect(renderer.root.findAllByType('a').map((a) => a.props.href)).toEqual([
      '/config',
      '/usage-analytics',
    ]);
    expect(renderer.root.findAllByType('button').map((b) => Boolean(b.props.disabled))).toEqual([
      false,
      true,
      true,
      true,
    ]);
    mocks.auth.role = 'admin';
    await act(async () => {
      renderer.update(<AccessLayout admin={page} />);
    });
    expect(renderer.root.findAllByType('button').map((b) => Boolean(b.props.disabled))).toEqual([
      false,
      false,
      false,
      false,
    ]);
  });
  it('disables server configuration toggles, but permits explicitly local view preferences', async () => {
    await act(async () => {
      renderer = create(
        <ReadOnlyContext.Provider value>
          <ToggleSwitch checked onChange={vi.fn()} />
          <ToggleSwitch checked writeAction={false} onChange={vi.fn()} />
        </ReadOnlyContext.Provider>
      );
    });
    expect(renderer.root.findAllByType('input').map((i) => Boolean(i.props.disabled))).toEqual([
      true,
      false,
    ]);
  });
  it('shows key status without allowing a viewer to rotate or disable keys', async () => {
    await act(async () => {
      renderer = create(
        <ReadOnlyContext.Provider value>
          <ReadonlyKeyCard base="http://local" />
        </ReadOnlyContext.Provider>
      );
    });
    expect(renderer.root.findAllByType('button').every((b) => b.props.disabled)).toBe(true);
    expect(renderer.root.findAllByType('code')).toHaveLength(0);
  });
  it('preserves one-time administrator key generation', async () => {
    await act(async () => {
      renderer = create(<ReadonlyKeyCard base="http://local" />);
    });
    await act(async () => renderer.root.findAllByType('button')[0].props.onClick());
    expect(renderer.root.findByType('code').children).toEqual(['local-test-key']);
  });
});

import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Button } from '@/components/ui/Button';
import { FeishuNotificationsCard } from './FeishuNotificationsCard';
const mocks = vi.hoisted(() => ({
  status: vi.fn(),
  update: vi.fn(),
  test: vi.fn(),
  t: (key: string) => key,
}));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: mocks.t }) }));
vi.mock('@/services/api/notifications', () => ({
  notificationsApi: mocks,
  notificationEvents: ['auth_invalid', 'auth_recovered', 'quota_exhausted', 'quota_recovered'],
}));
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
const saved = {
  enabled: true,
  locked: false,
  source: 'database',
  webhookConfigured: true,
  signingSecretConfigured: true,
  events: ['auth_invalid'],
  updatedAtMs: 0,
  deliveries: [],
};
let renderer: ReactTestRenderer;
const button = (label: string) =>
  renderer.root.findAllByType(Button).find((b) => b.props.children === label)!;
beforeEach(() => {
  vi.clearAllMocks();
  mocks.status.mockResolvedValue(saved);
  mocks.update.mockResolvedValue(saved);
  mocks.test.mockResolvedValue({ sent: true });
});
afterEach(async () => {
  if (renderer) await act(async () => renderer.unmount());
});
describe('Feishu administrator settings', () => {
  it('keeps existing credentials opaque and requires saving before testing', async () => {
    await act(async () => {
      renderer = create(<FeishuNotificationsCard base="http://local" />);
    });
    const fields = renderer.root.findAllByType('input').filter((i) => i.props.type === 'password');
    expect(fields.map((i) => i.props.value)).toEqual(['', '']);
    await act(async () =>
      fields[0].props.onChange({
        target: { value: 'https://open.feishu.cn/open-apis/bot/v2/hook/new-token' },
      })
    );
    expect(button('feishu.test').props.disabled).toBe(true);
    await act(async () => button('common.save').props.onClick());
    expect(mocks.update).toHaveBeenCalledWith(
      'http://local',
      expect.objectContaining({
        webhookUrl: 'https://open.feishu.cn/open-apis/bot/v2/hook/new-token',
      })
    );
    expect(mocks.update.mock.calls[0][1]).not.toHaveProperty('signingSecret');
    expect(fields[0].props.value).toBe('');
    await act(async () => button('feishu.test').props.onClick());
    expect(mocks.test).toHaveBeenCalledWith('http://local');
  });
  it('locks environment-managed settings while retaining explicit test sending', async () => {
    mocks.status.mockResolvedValue({ ...saved, locked: true, source: 'environment' });
    await act(async () => {
      renderer = create(<FeishuNotificationsCard base="http://local" />);
    });
    expect(renderer.root.findByType('fieldset').props.disabled).toBe(true);
    expect(button('common.save').props.disabled).toBe(true);
    expect(button('feishu.test').props.disabled).toBe(false);
  });
});

describe('notification configuration recovery and removal', () => {
  it('sends an explicit webhook removal and disables notification', async () => {
    await act(async () => {
      renderer = create(<FeishuNotificationsCard base="http://local" />);
    });
    const label = renderer.root
      .findAllByType('label')
      .find((item) => item.children.includes('feishu.clear_webhook'))!;
    await act(async () => label.findByType('input').props.onChange({ target: { checked: true } }));
    await act(async () => button('common.save').props.onClick());
    expect(mocks.update).toHaveBeenCalledWith(
      'http://local',
      expect.objectContaining({ clearWebhook: true, enabled: false })
    );
  });
  it('shows a stored-configuration failure and prevents overwriting it', async () => {
    mocks.status.mockResolvedValue({
      ...saved,
      enabled: false,
      configurationError: 'stored_notification_settings_unavailable',
    });
    await act(async () => {
      renderer = create(<FeishuNotificationsCard base="http://local" />);
    });
    expect(renderer.root.findByProps({ role: 'alert' }).children).toContain(
      'feishu.configuration_error'
    );
    expect(renderer.root.findByType('fieldset').props.disabled).toBe(true);
    expect(button('common.save').props.disabled).toBe(true);
    expect(button('feishu.test').props.disabled).toBe(true);
  });
});

describe('repairing stored notification settings', () => {
  it('allows editing an invalid stored webhook and saving the correction', async () => {
    mocks.status.mockResolvedValue({
      ...saved,
      enabled: false,
      configurationError: 'stored_notification_settings_invalid',
    });
    await act(async () => {
      renderer = create(<FeishuNotificationsCard base="http://local" />);
    });
    expect(renderer.root.findByType('fieldset').props.disabled).toBe(false);
    expect(renderer.root.findByProps({ role: 'alert' }).children).toContain(
      'feishu.configuration_invalid'
    );
    const field = renderer.root
      .findAllByType('input')
      .find((item) => item.props.type === 'password')!;
    await act(async () =>
      field.props.onChange({
        target: { value: 'https://open.feishu.cn/open-apis/bot/v2/hook/repaired' },
      })
    );
    await act(async () => button('common.save').props.onClick());
    expect(mocks.update).toHaveBeenCalledWith(
      'http://local',
      expect.objectContaining({
        webhookUrl: 'https://open.feishu.cn/open-apis/bot/v2/hook/repaired',
      })
    );
  });
  it('requires an explicit choice before replacing unreadable settings', async () => {
    mocks.status.mockResolvedValue({
      ...saved,
      enabled: false,
      webhookConfigured: false,
      signingSecretConfigured: false,
      configurationError: 'stored_notification_settings_unavailable',
    });
    await act(async () => {
      renderer = create(<FeishuNotificationsCard base="http://local" />);
    });
    expect(renderer.root.findByType('fieldset').props.disabled).toBe(true);
    const label = renderer.root
      .findAllByType('label')
      .find((item) => item.children.includes('feishu.replace_configuration'))!;
    await act(async () => label.findByType('input').props.onChange({ target: { checked: true } }));
    expect(renderer.root.findByType('fieldset').props.disabled).toBe(false);
    const field = renderer.root
      .findAllByType('input')
      .find((item) => item.props.type === 'password')!;
    await act(async () =>
      field.props.onChange({
        target: { value: 'https://open.feishu.cn/open-apis/bot/v2/hook/new' },
      })
    );
    await act(async () => button('common.save').props.onClick());
    expect(mocks.update).toHaveBeenCalledWith(
      'http://local',
      expect.objectContaining({
        replaceConfiguration: true,
        enabled: false,
        events: ['auth_invalid'],
        signingSecret: '',
        webhookUrl: 'https://open.feishu.cn/open-apis/bot/v2/hook/new',
      })
    );
  });
});

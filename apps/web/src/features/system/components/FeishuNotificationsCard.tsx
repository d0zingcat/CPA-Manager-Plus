import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Card } from '@/components/ui/Card';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import {
  notificationsApi,
  notificationEvents,
  type NotificationEvent,
  type FeishuStatus,
} from '@/services/api/notifications';
import styles from './FeishuNotificationsCard.module.scss';

export function FeishuNotificationsCard({ base }: { base: string }) {
  const { t } = useTranslation();
  const [status, setStatus] = useState<FeishuStatus | null>(null);
  const [enabled, setEnabled] = useState(false);
  const [events, setEvents] = useState<NotificationEvent[]>([]);
  const [url, setUrl] = useState('');
  const [secret, setSecret] = useState('');
  const [clearSecret, setClearSecret] = useState(false);
  const [clearWebhook, setClearWebhook] = useState(false);
  const [replaceConfiguration, setReplaceConfiguration] = useState(false);
  const [dirty, setDirty] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const apply = (value: FeishuStatus) => {
    setStatus(value);
    setEnabled(value.enabled);
    setEvents(value.events);
    setUrl('');
    setSecret('');
    setClearSecret(false);
    setClearWebhook(false);
    setReplaceConfiguration(false);
    setDirty(false);
  };
  useEffect(() => {
    let active = true;
    setStatus(null);
    setError('');
    setUrl('');
    setSecret('');
    notificationsApi
      .status(base)
      .then((value) => {
        if (active) apply(value);
      })
      .catch((err: unknown) => {
        if (active) setError(err instanceof Error ? err.message : t('feishu.load_failed'));
      });
    return () => {
      active = false;
    };
  }, [base, t]);
  const change = () => {
    setDirty(true);
    setNotice('');
  };
  const perform = async (test: boolean) => {
    setBusy(true);
    setError('');
    setNotice('');
    try {
      if (test) {
        await notificationsApi.test(base);
        setStatus(await notificationsApi.status(base));
        setNotice('feishu.test_success');
      } else {
        apply(
          await notificationsApi.update(base, {
            enabled,
            events,
            ...(url.trim() || replaceConfiguration ? { webhookUrl: url.trim() } : {}),
            ...(secret.trim() || replaceConfiguration ? { signingSecret: secret.trim() } : {}),
            clearSigningSecret: clearSecret,
            clearWebhook,
            replaceConfiguration,
          })
        );
        setNotice('feishu.saved');
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : t('feishu.operation_failed'));
      if (test) {
        try {
          setStatus(await notificationsApi.status(base));
        } catch {
          /* Keep the original delivery error visible. */
        }
      }
    } finally {
      setBusy(false);
    }
  };
  const unreadable = status?.configurationError === 'stored_notification_settings_unavailable';
  const locked = status?.locked === true || (unreadable && !replaceConfiguration);
  return (
    <Card title={t('feishu.title')}>
      <div className={styles.content}>
        <p>{t('feishu.description')}</p>
        {error && (
          <div className="error-box" role="alert">
            {error}
          </div>
        )}
        {status?.configurationError && (
          <div className="error-box" role="alert">
            {t(unreadable ? 'feishu.configuration_error' : 'feishu.configuration_invalid')}
          </div>
        )}
        {notice && <p role="status">{t(notice)}</p>}
        {!status && !error && <p>{t('common.loading')}</p>}
        {status && (
          <>
            <p className={styles.source}>
              {t(status.locked ? 'feishu.source_environment' : 'feishu.source_database')}
            </p>
            {unreadable && !status.locked && (
              <label className={styles.check}>
                <input
                  type="checkbox"
                  checked={replaceConfiguration}
                  disabled={busy}
                  onChange={(e) => {
                    setReplaceConfiguration(e.target.checked);
                    change();
                  }}
                />
                {t('feishu.replace_configuration')}
              </label>
            )}
            <fieldset disabled={busy || locked} className={styles.fields}>
              <label className={styles.check}>
                <input
                  type="checkbox"
                  checked={enabled}
                  disabled={clearWebhook}
                  onChange={(e) => {
                    setEnabled(e.target.checked);
                    change();
                  }}
                />
                {t('feishu.enabled')}
              </label>
              <div className={styles.grid}>
                <Input
                  label={t('feishu.webhook')}
                  disabled={clearWebhook}
                  type="password"
                  autoComplete="off"
                  value={url}
                  placeholder={t(
                    status.webhookConfigured
                      ? 'feishu.configured_keep'
                      : 'feishu.webhook_placeholder'
                  )}
                  onChange={(e) => {
                    setUrl(e.target.value);
                    change();
                  }}
                />
                <Input
                  label={t('feishu.secret')}
                  type="password"
                  autoComplete="new-password"
                  disabled={clearSecret || clearWebhook}
                  value={secret}
                  placeholder={t(
                    status.signingSecretConfigured ? 'feishu.configured_keep' : 'feishu.optional'
                  )}
                  onChange={(e) => {
                    setSecret(e.target.value);
                    change();
                  }}
                />
              </div>
              {(status.webhookConfigured || unreadable) && (
                <label className={styles.check}>
                  <input
                    type="checkbox"
                    checked={clearWebhook}
                    onChange={(e) => {
                      setClearWebhook(e.target.checked);
                      if (e.target.checked) {
                        setEnabled(false);
                        setUrl('');
                        setSecret('');
                      }
                      change();
                    }}
                  />
                  {t('feishu.clear_webhook')}
                </label>
              )}
              {status.signingSecretConfigured && (
                <label className={styles.check}>
                  <input
                    type="checkbox"
                    checked={clearSecret}
                    onChange={(e) => {
                      setClearSecret(e.target.checked);
                      change();
                    }}
                  />
                  {t('feishu.clear_secret')}
                </label>
              )}
              <p>{t('feishu.events')}</p>
              <div className={styles.events}>
                {notificationEvents.map((event) => (
                  <label className={styles.check} key={event}>
                    <input
                      type="checkbox"
                      checked={events.includes(event)}
                      onChange={(e) => {
                        setEvents((current) =>
                          e.target.checked
                            ? [...current, event]
                            : current.filter((item) => item !== event)
                        );
                        change();
                      }}
                    />
                    {t(`feishu.event_${event}`)}
                  </label>
                ))}
              </div>
            </fieldset>
            <p className={styles.hint}>{t('feishu.test_hint')}</p>
            <div className={styles.actions}>
              <Button disabled={busy || locked || !dirty} onClick={() => void perform(false)}>
                {t('common.save')}
              </Button>
              <Button
                variant="secondary"
                disabled={busy || dirty || !status.enabled || !status.webhookConfigured}
                onClick={() => void perform(true)}
              >
                {t('feishu.test')}
              </Button>
              <Button
                variant="secondary"
                disabled={busy || dirty}
                onClick={() => {
                  setBusy(true);
                  void notificationsApi
                    .status(base)
                    .then(apply)
                    .catch((err: unknown) =>
                      setError(err instanceof Error ? err.message : t('feishu.load_failed'))
                    )
                    .finally(() => setBusy(false));
                }}
              >
                {t('common.refresh')}
              </Button>
            </div>
            <h4>{t('feishu.history')}</h4>
            {status.deliveries.length === 0 ? (
              <p className={styles.hint}>{t('feishu.empty')}</p>
            ) : (
              <div className={styles.table}>
                <table>
                  <thead>
                    <tr>
                      <th>{t('feishu.time')}</th>
                      <th>{t('feishu.events')}</th>
                      <th>{t('feishu.status')}</th>
                      <th>{t('feishu.attempts')}</th>
                      <th>{t('feishu.error')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {status.deliveries.map((item) => (
                      <tr key={item.id}>
                        <td>{new Date(item.createdAtMs).toLocaleString()}</td>
                        <td>{t(`feishu.event_${item.event}`)}</td>
                        <td>{t(`feishu.status_${item.status}`)}</td>
                        <td>{item.attempts}</td>
                        <td>{item.error || '—'}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </>
        )}
      </div>
    </Card>
  );
}

import { useReadOnly } from './readOnlyState';
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { accessApi, type ReadonlyKeyStatus } from '@/services/api/access';
import styles from './ReadonlyKeyCard.module.scss';

export function ReadonlyKeyCard({ base }: { base: string }) {
  const { t } = useTranslation();
  const readOnly = useReadOnly();
  const [status, setStatus] = useState<ReadonlyKeyStatus | null>(null);
  const [key, setKey] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  useEffect(() => {
    let active = true;
    accessApi
      .keyStatus(base)
      .then((value) => {
        if (active) setStatus(value);
      })
      .catch((err: unknown) => {
        if (active) setError(err instanceof Error ? err.message : String(err));
      });
    return () => {
      active = false;
    };
  }, [base]);
  const update = async (disable: boolean) => {
    setBusy(true);
    setError('');
    setKey('');
    try {
      if (disable) await accessApi.disableKey(base);
      else {
        const result = await accessApi.rotateKey(base);
        setKey(result.key);
      }
      setStatus(await accessApi.keyStatus(base));
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <section className={styles.card}>
      <h3>{t('readonly.key_title')}</h3>
      <p>{t('readonly.key_description')}</p>
      {status && (
        <p>
          {t(status.enabled ? 'readonly.enabled' : 'readonly.disabled')}
          {status.updatedAtMs > 0 && ` · ${new Date(status.updatedAtMs).toLocaleString()}`}
        </p>
      )}
      {error && <p role="alert">{error}</p>}
      <div className={styles.actions}>
        <Button disabled={readOnly || busy || !status} onClick={() => void update(false)}>
          {t(status?.enabled ? 'readonly.rotate' : 'readonly.generate')}
        </Button>
        <Button
          variant="danger"
          disabled={readOnly || busy || !status?.enabled}
          onClick={() => void update(true)}
        >
          {t('readonly.disable')}
        </Button>
      </div>
      {key && (
        <div className={styles.secret}>
          <p>{t('readonly.once')}</p>
          <code>{key}</code>
          <Button
            variant="secondary"
            onClick={() => {
              void navigator.clipboard
                .writeText(key)
                .catch(() => setError(t('readonly.copy_error')));
            }}
          >
            {t('readonly.copy')}
          </Button>
        </div>
      )}
    </section>
  );
}

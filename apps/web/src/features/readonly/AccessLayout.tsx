import type { ReactElement } from 'react';
import { useAuthStore } from '@/stores/useAuthStore';
import { ReadOnlyContext } from './readOnlyState';

export function AccessLayout({ admin }: { admin: ReactElement }) {
  const readOnly = useAuthStore((state) => state.role === 'usage_viewer');
  const key = useAuthStore((state) => state.managementKey);
  return (
    <ReadOnlyContext.Provider key={key} value={readOnly}>
      {admin}
    </ReadOnlyContext.Provider>
  );
}

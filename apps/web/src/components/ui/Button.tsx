import { useReadOnly } from '@/features/readonly/readOnlyState';
import type { ButtonHTMLAttributes, PropsWithChildren } from 'react';

type ButtonVariant = 'primary' | 'secondary' | 'ghost' | 'danger';
type ButtonSize = 'md' | 'sm' | 'xs';

interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  size?: ButtonSize;
  fullWidth?: boolean;
  iconOnly?: boolean;
  loading?: boolean;
  writeAction?: boolean;
}

export function Button({
  children,
  variant = 'primary',
  size = 'md',
  fullWidth = false,
  iconOnly = false,
  loading = false,
  writeAction = false,
  className = '',
  disabled,
  ...rest
}: PropsWithChildren<ButtonProps>) {
  const readOnly = useReadOnly();
  const hasChildren = children !== null && children !== undefined && children !== false;
  const classes = [
    'btn',
    `btn-${variant}`,
    size !== 'md' ? `btn-${size}` : '',
    iconOnly ? 'btn-icon-only' : '',
    fullWidth ? 'btn-full' : '',
    className,
  ]
    .filter(Boolean)
    .join(' ');

  return (
    <button
      className={classes}
      disabled={disabled || loading || (readOnly && (writeAction || variant === 'danger'))}
      {...rest}
    >
      {loading && <span className="loading-spinner" aria-hidden="true" />}
      {hasChildren && <span>{children}</span>}
    </button>
  );
}

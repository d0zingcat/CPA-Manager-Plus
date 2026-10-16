import { forwardRef, type ButtonHTMLAttributes } from 'react';
import { useReadOnly } from './readOnlyState';

/** Native action buttons use the same permission as the shared Button. */
export const ReadOnlyButton = forwardRef<
  HTMLButtonElement,
  ButtonHTMLAttributes<HTMLButtonElement>
>(function ReadOnlyButton({ disabled, ...props }, ref) {
  const readOnly = useReadOnly();
  return <button {...props} ref={ref} disabled={disabled || readOnly} />;
});

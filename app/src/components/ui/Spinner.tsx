export type SpinnerSize = 'xs' | 'sm' | 'md' | 'lg';

const SIZE_MAP: Record<SpinnerSize, string> = {
  xs: 'h-3 w-3 border',
  sm: 'h-4 w-4 border-2',
  md: 'h-5 w-5 border-2',
  lg: 'h-6 w-6 border-2',
};

interface SpinnerProps {
  size?: SpinnerSize;
  className?: string;
}

export function Spinner({ size = 'md', className = '' }: SpinnerProps) {
  return (
    <span
      data-id="spinner"
      role="status"
      aria-label="Loading"
      className={`inline-block animate-spin rounded-full border-vsc-accent/30 border-t-vsc-accent ${SIZE_MAP[size]} ${className}`}
    />
  );
}

interface LoadingOverlayProps {
  text?: string;
  fullscreen?: boolean;
  className?: string;
  size?: SpinnerSize;
}

export function LoadingOverlay({ text, fullscreen = false, className = '', size = 'lg' }: LoadingOverlayProps) {
  const wrap = fullscreen
    ? 'fixed inset-0 z-50 bg-[#0A0A0A]'
    : 'absolute inset-0 bg-vsc-bg/60 backdrop-blur-sm';
  return (
    <div data-id="loading-overlay" className={`${wrap} flex flex-col items-center justify-center gap-3 ${className}`}>
      <Spinner size={size} />
      {text ? <span data-id="loading-overlay-text" className="text-sm text-zinc-500">{text}</span> : null}
    </div>
  );
}

interface LoadingInlineProps {
  text?: string;
  size?: SpinnerSize;
  className?: string;
}

export function LoadingInline({ text, size = 'md', className = '' }: LoadingInlineProps) {
  return (
    <div data-id="loading-inline" className={`flex items-center justify-center gap-2 ${className}`}>
      <Spinner size={size} />
      {text ? <span data-id="loading-inline-text" className="text-sm text-zinc-500">{text}</span> : null}
    </div>
  );
}

export default Spinner;

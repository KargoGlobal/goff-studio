import { cn } from '@/lib/cn'
import type { ButtonHTMLAttributes, HTMLAttributes, InputHTMLAttributes } from 'react'

export function Button({
  className,
  variant = 'default',
  size = 'md',
  ...props
}: ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: 'default' | 'outline' | 'ghost' | 'danger'
  size?: 'sm' | 'md'
}) {
  return (
    <button
      className={cn(
        'inline-flex shrink-0 items-center justify-center gap-2 whitespace-nowrap rounded-md font-medium transition-colors',
        'disabled:pointer-events-none disabled:opacity-50',
        size === 'sm' ? 'h-8 px-3 text-[13px]' : 'h-9 px-4 text-sm',
        variant === 'default' && 'bg-brand text-white hover:opacity-90',
        variant === 'outline' && 'border bg-surface text-ink hover:bg-canvas',
        variant === 'ghost' && 'text-ink-soft hover:bg-canvas hover:text-ink',
        variant === 'danger' && 'bg-danger text-white hover:opacity-90',
        className,
      )}
      {...props}
    />
  )
}

export function Card({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return <div className={cn('rounded-xl border bg-surface', className)} {...props} />
}

export function Input({ className, ...props }: InputHTMLAttributes<HTMLInputElement>) {
  return (
    <input
      className={cn(
        'h-9 w-full rounded-md border bg-surface px-3 text-sm text-ink',
        'placeholder:text-ink-muted focus:border-brand focus:outline-none',
        className,
      )}
      {...props}
    />
  )
}

export function Badge({
  className,
  tone = 'neutral',
  ...props
}: HTMLAttributes<HTMLSpanElement> & { tone?: 'neutral' | 'ok' | 'warn' | 'danger' | 'brand' }) {
  return (
    <span
      className={cn(
        'inline-flex items-center rounded-full px-2 py-0.5 text-[11px] font-medium',
        tone === 'neutral' && 'bg-canvas text-ink-soft',
        tone === 'ok' && 'bg-ok-soft text-ok',
        tone === 'warn' && 'bg-warn-soft text-warn',
        tone === 'danger' && 'bg-danger-soft text-danger',
        tone === 'brand' && 'bg-brand-soft text-brand',
        className,
      )}
      {...props}
    />
  )
}

export function Toggle({
  checked,
  onChange,
  disabled,
  label,
  busy,
}: {
  checked: boolean
  onChange: (next: boolean) => void
  disabled?: boolean
  label: string
  busy?: boolean
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      disabled={disabled || busy}
      onClick={() => onChange(!checked)}
      className={cn(
        'relative inline-flex h-5 w-9 shrink-0 items-center rounded-full transition-colors',
        checked ? 'bg-brand' : 'bg-line',
        (disabled || busy) && 'cursor-not-allowed opacity-50',
      )}
    >
      <span
        className={cn(
          'ml-0.5 h-4 w-4 rounded-full bg-white shadow transition-transform',
          checked && 'translate-x-4',
        )}
      />
    </button>
  )
}

export function Spinner({ className }: { className?: string }) {
  return (
    <span
      className={cn(
        'inline-block h-4 w-4 animate-spin rounded-full border-2 border-line border-t-brand',
        className,
      )}
    />
  )
}

export function Code({ className, ...props }: HTMLAttributes<HTMLElement>) {
  return (
    <code
      className={cn(
        'rounded bg-canvas px-1.5 py-0.5 font-mono text-[12.5px] text-ink-soft',
        className,
      )}
      {...props}
    />
  )
}

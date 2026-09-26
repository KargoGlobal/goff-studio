import { useEffect, type ReactNode } from 'react'
import { cn } from '@/lib/cn'

export function Dialog({
  open,
  onClose,
  title,
  children,
  footer,
  tone = 'neutral',
}: {
  open: boolean
  onClose: () => void
  title: string
  children: ReactNode
  footer?: ReactNode
  tone?: 'neutral' | 'protected' | 'danger'
}) {
  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [open, onClose])

  if (!open) return null

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
      <div
        className="absolute inset-0 bg-black/40"
        onClick={onClose}
        aria-hidden="true"
      />
      <div
        role="dialog"
        aria-modal="true"
        aria-label={title}
        className={cn(
          'relative z-10 w-full max-w-lg overflow-hidden rounded-xl border bg-surface shadow-xl',
          tone === 'protected' && 'border-warn',
          tone === 'danger' && 'border-[color:var(--color-danger-neon)]',
        )}
      >
        <div
          className={cn(
            'border-b px-5 py-3.5',
            tone === 'protected' && 'border-warn bg-warn-soft',
            tone === 'danger' && 'border-[color:var(--color-danger-neon)] bg-[color:var(--color-danger-soft)] text-[color:var(--color-danger)]',
          )}
        >
          <h2 className="text-[15px] font-semibold text-ink">{title}</h2>
        </div>
        <div className="max-h-[60vh] overflow-y-auto px-5 py-4">{children}</div>
        {footer && <div className="flex justify-end gap-2 border-t px-5 py-3.5">{footer}</div>}
      </div>
    </div>
  )
}

import { type ReactNode } from 'react'
import { cn } from '@/lib/cn'
import { useOverlay } from '@/hooks/useOverlay'

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
  useOverlay(open, onClose)

  if (!open) return null

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 max-md:items-end max-md:p-0">
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
          'max-md:flex max-md:max-h-[92dvh] max-md:max-w-none max-md:flex-col max-md:rounded-b-none max-md:border-x-0 max-md:border-b-0',
          tone === 'protected' && 'border-warn',
          tone === 'danger' && 'border-[color:var(--color-danger-neon)]',
        )}
      >
        <div
          className={cn(
            'border-b px-5 py-3.5 max-md:shrink-0',
            tone === 'protected' && 'border-warn bg-warn-soft',
            tone === 'danger' && 'border-[color:var(--color-danger-neon)] bg-[color:var(--color-danger-soft)] text-[color:var(--color-danger)]',
          )}
        >
          <h2 className="text-[15px] font-semibold text-ink">{title}</h2>
        </div>
        <div className="max-h-[60vh] overflow-y-auto px-5 py-4 max-md:max-h-none max-md:min-h-0 max-md:flex-1 max-md:overscroll-contain">{children}</div>
        {footer && (
          <div className="flex justify-end gap-2 border-t px-5 py-3.5 max-md:shrink-0 max-md:pb-[max(0.875rem,env(safe-area-inset-bottom))] max-md:[&>*]:flex-1">
            {footer}
          </div>
        )}
      </div>
    </div>
  )
}

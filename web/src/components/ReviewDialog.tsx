import { useState } from 'react'
import { ChevronRight, ShieldAlert } from 'lucide-react'
import { cn } from '@/lib/cn'
import { Button, Spinner } from '@/components/ui/primitives'
import { Dialog } from '@/components/ui/Dialog'
import type { DiffResult } from '@/lib/api'

export function ReviewDialog({
  open,
  onClose,
  onConfirm,
  title,
  diff,
  loading,
  saving,
  protectedEnv,
  envName,
  danger,
}: {
  open: boolean
  onClose: () => void
  onConfirm: () => void
  title: string
  diff?: DiffResult
  loading: boolean
  saving: boolean
  protectedEnv: boolean
  envName: string
  danger?: boolean
}) {
  const [showDiff, setShowDiff] = useState(false)
  const [typed, setTyped] = useState('')

  const needsTyping = protectedEnv
  const canConfirm = !saving && !loading && (!needsTyping || typed === envName)

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={title}
      tone={danger ? 'danger' : protectedEnv ? 'protected' : 'neutral'}
      footer={
        <>
          <Button variant="outline" onClick={onClose} disabled={saving}>
            Cancel
          </Button>
          <Button
            variant={danger ? 'danger' : 'default'}
            onClick={onConfirm}
            disabled={!canConfirm}
          >
            {saving && <Spinner className="border-white/40 border-t-white" />}
            {saving ? 'Saving' : danger ? 'Delete' : 'Save change'}
          </Button>
        </>
      }
    >
      {loading ? (
        <div className="flex items-center gap-2 text-ink-muted">
          <Spinner />
          <span>Working out what will change…</span>
        </div>
      ) : (
        <div className="space-y-4">
          <p className="text-[15px] text-ink">{diff?.description}</p>

          {protectedEnv && (
            <div
              className={cn(
                'rounded-lg border p-3',
                danger
                  ? 'border-[color:var(--color-danger-neon)] bg-[color:var(--color-danger-soft)]'
                  : 'border-warn bg-warn-soft',
              )}
            >
              <p className="flex items-start gap-2 text-[13px] text-ink">
                <ShieldAlert
                  className={cn(
                    'mt-0.5 h-4 w-4 shrink-0',
                    danger ? 'text-[color:var(--color-danger)]' : 'text-warn',
                  )}
                />
                <span>
                  This is a protected environment. Type <strong>{envName}</strong> to confirm.
                </span>
              </p>
              <input
                value={typed}
                onChange={(e) => setTyped(e.target.value)}
                placeholder={envName}
                aria-label={`Type ${envName} to confirm`}
                className={cn(
                  'mt-2 h-8 w-full rounded-md border bg-surface px-2.5 font-mono text-[13px] focus:outline-none',
                  danger
                    ? 'focus:border-[color:var(--color-danger-neon)]'
                    : 'focus:border-brand',
                )}
              />
            </div>
          )}

          {diff?.diff && (
            <div>
              <button
                type="button"
                onClick={() => setShowDiff((s) => !s)}
                className="flex items-center gap-1 text-[12.5px] text-ink-muted hover:text-ink"
              >
                <ChevronRight className={cn('h-3.5 w-3.5 transition-transform', showDiff && 'rotate-90')} />
                {showDiff ? 'Hide' : 'Show'} file changes
              </button>
              {showDiff && (
                <pre className="mt-2 max-h-56 overflow-auto rounded-lg border border-[color:var(--color-brand)] bg-surface p-3 font-mono text-[12px] leading-relaxed">
                  {diff.diff.split('\n').map((line, i) => (
                    <div
                      key={i}
                      className={cn(
                        line.startsWith('+') && 'text-ok',
                        line.startsWith('-') && 'text-danger',
                        line.startsWith('@@') && 'text-ink-muted italic',
                      )}
                    >
                      {line || ' '}
                    </div>
                  ))}
                </pre>
              )}
            </div>
          )}
        </div>
      )}
    </Dialog>
  )
}

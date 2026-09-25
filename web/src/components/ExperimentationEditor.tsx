import { useState } from 'react'
import type { Experimentation } from '@/lib/api'
import { Button } from '@/components/ui/primitives'
import { DateTimeInput } from '@/components/DateTimeInput'
import { DATE_HINT, formatWhen } from '@/lib/dates'

export function describeExperimentation(e: Experimentation): string {
  if (e.start && e.end) {
    return `On only between ${formatWhen(e.start)} and ${formatWhen(e.end)}`
  }
  if (e.start) return `Off until ${formatWhen(e.start)}, then on`
  if (e.end) return `On until ${formatWhen(e.end)}, then off`
  return 'No window set'
}

export function ExperimentationEditor({
  window,
  disabled,
  onSave,
  onRemove,
}: {
  window?: Experimentation
  disabled?: boolean
  onSave: (next: Experimentation) => void
  onRemove: () => void
}) {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState<Experimentation>(window ?? {})
  const [error, setError] = useState<string | null>(null)

  function open() {
    setDraft(window ?? {})
    setError(null)
    setEditing(true)
  }

  if (!editing) {
    return (
      <div className="space-y-2">
        <p className="text-[13px] text-ink">
          {window ? describeExperimentation(window) : 'No schedule, so the flag is never auto-disabled.'}
        </p>
        {!disabled && (
          <div className="flex gap-2">
            <Button size="sm" variant="outline" onClick={open}>
              {window ? 'Edit window' : 'Add window'}
            </Button>
            {window && (
              <Button size="sm" variant="outline" onClick={onRemove}>
                Remove window
              </Button>
            )}
          </div>
        )}
      </div>
    )
  }

  function bound(label: 'start' | 'end') {
    return (
      <div className="flex items-center gap-2">
        <span className="w-10 text-[12px] capitalize text-ink-muted">{label}</span>
        <DateTimeInput
          label={`Experimentation ${label}`}
          value={draft[label] ?? ''}
          onChange={(iso) => setDraft((prev) => ({ ...prev, [label]: iso }))}
        />
      </div>
    )
  }

  return (
    <div className="space-y-2">
      <div className="space-y-1.5 rounded-md border bg-surface p-2.5">
        {bound('start')}
        {bound('end')}
        <p className="text-[11px] text-ink-muted">
          Leave one side empty for an open-ended window. Outside it the flag is off for everyone.
        </p>
      </div>

      {error && (
        <p role="alert" className="text-[12.5px] text-danger">
          {error}
        </p>
      )}

      <div className="flex gap-2">
        <Button
          size="sm"
          onClick={() => {
            const next: Experimentation = { start: draft.start || '', end: draft.end || '' }
            if (!next.start && !next.end) {
              setError(`Set a start date, an end date, or both (${DATE_HINT}).`)
              return
            }
            if (next.start && next.end && new Date(next.end) <= new Date(next.start)) {
              setError('The end date must be after the start date.')
              return
            }
            setError(null)
            onSave(next)
            setEditing(false)
          }}
        >
          Review change
        </Button>
        <Button size="sm" variant="outline" onClick={() => setEditing(false)}>
          Cancel
        </Button>
      </div>
    </div>
  )
}

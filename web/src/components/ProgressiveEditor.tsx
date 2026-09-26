import { useState } from 'react'
import type { ProgressiveRollout, RolloutStep } from '@/lib/api'
import { Button } from '@/components/ui/primitives'
import { Select } from '@/components/ui/Select'
import { DateTimeInput } from '@/components/DateTimeInput'
import { DATE_HINT, formatWhen } from '@/lib/dates'

export function describeProgressive(p: ProgressiveRollout): string {
  return `${p.initial.percentage}% ${p.initial.variation} on ${formatWhen(p.initial.date)}, ramping to ${p.end.percentage}% ${p.end.variation} by ${formatWhen(p.end.date)}`
}

export function ProgressiveEditor({
  rollout,
  variations,
  disabled,
  onSave,
  onRemove,
}: {
  rollout: ProgressiveRollout
  variations: string[]
  disabled?: boolean
  onSave: (next: ProgressiveRollout) => void
  onRemove: (variation: string) => void
}) {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState<ProgressiveRollout>(rollout)
  const [error, setError] = useState<string | null>(null)

  if (!editing) {
    return (
      <div className="space-y-2">
        <p className="text-[13px] text-ink">{describeProgressive(rollout)}</p>
        {!disabled && (
          <div className="flex gap-2">
            <Button
              size="sm"
              variant="outline"
              onClick={() => {
                setDraft(rollout)
                setError(null)
                setEditing(true)
              }}
            >
              Edit ramp
            </Button>
            <Button
              size="sm"
              variant="outline"
              onClick={() => onRemove(rollout.end.variation || variations[0] || '')}
            >
              Remove ramp
            </Button>
          </div>
        )}
      </div>
    )
  }

  function step(label: 'initial' | 'end', value: RolloutStep) {
    return (
      <div className="space-y-2 rounded-lg border bg-surface p-4">
        <p className="text-xs font-semibold uppercase tracking-wide text-[color:var(--color-sidebar-active)]">
          {label === 'initial' ? 'Start' : 'End'}
        </p>
        <div className="flex flex-wrap items-center gap-2">
          <div className="w-40">
            <Select
              value={value.variation}
              onChange={(v) =>
                setDraft((prev) => ({ ...prev, [label]: { ...value, variation: v } }))
              }
              ariaLabel={`${label} variation`}
              options={variations.map((v) => ({ value: v, label: v }))}
            />
          </div>
          <label className="inline-flex h-11 items-center gap-1 rounded-md border bg-surface pl-3 pr-2 focus-within:border-brand">
            <input
              type="number"
              min={0}
              max={100}
              aria-label={`${label} percentage`}
              value={String(value.percentage)}
              onChange={(e) =>
                setDraft((prev) => ({
                  ...prev,
                  [label]: { ...value, percentage: Number(e.target.value) },
                }))
              }
              className="w-16 bg-transparent font-mono text-sm text-ink focus:outline-none"
            />
            <span className="text-sm text-ink-muted">%</span>
          </label>
          <DateTimeInput
            className="w-64"
            label={`${label} date`}
            value={value.date}
            onChange={(iso) =>
              setDraft((prev) => ({ ...prev, [label]: { ...value, date: iso } }))
            }
          />
        </div>
      </div>
    )
  }

  return (
    <div className="space-y-2">
      {step('initial', draft.initial)}
      {step('end', draft.end)}

      {error && (
        <p role="alert" className="text-[12.5px] text-danger">
          {error}
        </p>
      )}

      <div className="flex gap-2">
        <Button
          size="sm"
          onClick={() => {
            if (!draft.initial.date || !draft.end.date) {
              setError(`Both dates are required (${DATE_HINT}).`)
              return
            }
            if (new Date(draft.end.date) <= new Date(draft.initial.date)) {
              setError('The end date must be after the start date.')
              return
            }
            setError(null)
            onSave(draft)
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

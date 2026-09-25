import { useState } from 'react'
import type { ProgressiveRollout, RolloutStep } from '@/lib/api'
import { Button, Input } from '@/components/ui/primitives'
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
      <div className="space-y-1.5 rounded-md border bg-surface p-2.5">
        <p className="text-[11px] font-medium uppercase tracking-wide text-ink-muted">
          {label === 'initial' ? 'Start' : 'End'}
        </p>
        <div className="flex flex-wrap items-center gap-2">
          <select
            aria-label={`${label} variation`}
            value={value.variation}
            onChange={(e) =>
              setDraft((prev) => ({ ...prev, [label]: { ...value, variation: e.target.value } }))
            }
            className="h-8 rounded-md border bg-surface px-2 font-mono text-[12.5px] focus:border-brand focus:outline-none"
          >
            {variations.map((v) => (
              <option key={v} value={v}>
                {v}
              </option>
            ))}
          </select>
          <div className="flex items-center gap-1">
            <Input
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
              className="h-8 w-20 font-mono"
            />
            <span className="text-[12.5px] text-ink-muted">%</span>
          </div>
          <DateTimeInput
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

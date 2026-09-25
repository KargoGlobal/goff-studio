import { useState } from 'react'
import { FlaskConical } from 'lucide-react'
import type { Allocation, EvalResult, Experiment, ExperimentEdit } from '@/lib/api'
import { Badge, Button, Code, Input } from '@/components/ui/primitives'
import { formatWhen } from '@/lib/dates'
import {
  armsFromWeights,
  clampExposure,
  describeReason,
  exposureIntent,
  exposureStep,
  formatPercent,
  summarizeAllocation,
  windowState,
} from '@/lib/experiment'

const windowTone = {
  'not-started': 'neutral',
  running: 'ok',
  ended: 'warn',
  always: 'brand',
} as const

const windowLabel = {
  'not-started': 'not started',
  running: 'running',
  ended: 'ended',
  always: 'no window',
} as const

function nowISO() {
  return new Date().toISOString().replace(/\.\d{3}Z$/, 'Z')
}

export function ExperimentPanel({
  flagKey,
  ruleName,
  experiment,
  allocation,
  canRollout,
  canEditRules,
  preview,
  onReview,
}: {
  flagKey: string
  ruleName: string
  experiment: Experiment
  allocation: Allocation
  canRollout: boolean
  canEditRules: boolean
  preview: EvalResult | null
  onReview: (edit: ExperimentEdit) => void
}) {
  const total = experiment.totalShards
  const summary = summarizeAllocation(allocation, total)
  const current = summary.exposurePercent
  const [draft, setDraft] = useState(current)
  const [rerandomize, setRerandomize] = useState(false)
  const state = windowState(allocation)
  const key = allocation.experimentKey || `${flagKey}-${ruleName}`

  const exposure = clampExposure(draft, current, rerandomize)
  const intent = exposureIntent(exposure, current, rerandomize)
  const canChangeExposure = rerandomize ? canEditRules : canRollout && summary.rampable

  function setExposure(value: number) {
    if (Number.isFinite(value)) setDraft(clampExposure(value, current, rerandomize))
  }

  function review() {
    if (intent === 'grow') {
      onReview({ op: 'exposure', ruleName, exposurePercent: exposure })
    } else if (intent === 'rerandomize') {
      onReview({ op: 'rerandomize', ruleName, confirm: true, exposurePercent: exposure })
    }
  }

  const inPreview = preview && !preview.error && preview.allocation === ruleName

  return (
    <section
      aria-label={`Experiment on ${ruleName}`}
      className="mb-3 space-y-3 rounded-lg border border-brand bg-brand-soft p-3"
    >
      <div className="flex flex-wrap items-center gap-2">
        <FlaskConical className="h-3.5 w-3.5 text-brand" />
        <span className="text-[12px] font-semibold uppercase tracking-wide text-ink-muted">
          Experiment
        </span>
        <Code>{key}</Code>
        <Badge tone={windowTone[state]}>{windowLabel[state]}</Badge>
        {allocation.doLog === false && <Badge tone="neutral">not logged</Badge>}
      </div>

      <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-[12.5px]">
        <dt className="text-ink-muted">Unit</dt>
        <dd>
          {experiment.unit.type} · <Code>{experiment.unit.key}</Code>
          {experiment.unit.type === 'request' && (
            <span className="text-ink-muted"> (every request is assigned independently)</span>
          )}
        </dd>
        <dt className="text-ink-muted">Window</dt>
        <dd className="flex flex-wrap items-center gap-2">
          <span>
            {allocation.startAt ? formatWhen(allocation.startAt) : 'open start'} –{' '}
            {allocation.endAt ? formatWhen(allocation.endAt) : 'open end'}
          </span>
          {canRollout && state === 'not-started' && (
            <Button
              size="sm"
              variant="outline"
              onClick={() => onReview({ op: 'window', ruleName, startAt: nowISO() })}
            >
              Start now
            </Button>
          )}
          {canRollout && (state === 'running' || state === 'always') && (
            <Button
              size="sm"
              variant="outline"
              onClick={() => onReview({ op: 'window', ruleName, endAt: nowISO() })}
            >
              Stop now
            </Button>
          )}
          {canRollout && state === 'ended' && (
            <Button
              size="sm"
              variant="outline"
              onClick={() => onReview({ op: 'window', ruleName, endAt: '' })}
            >
              Reopen
            </Button>
          )}
        </dd>
        <dt className="text-ink-muted">Not exposed</dt>
        <dd>
          {allocation.passThrough === false
            ? "gets this rule's own variation"
            : 'continues to the next rule'}
        </dd>
      </dl>

      <div>
        <div className="mb-1 flex items-center justify-between">
          <label htmlFor={`exposure-${ruleName}`} className="text-[12px] text-ink-soft">
            Exposure of matching subjects
          </label>
          <span className="font-mono text-[12.5px]">{exposure}%</span>
        </div>
        <div className="flex items-center gap-3">
          <input
            type="range"
            min={0}
            max={100}
            step={exposureStep(total)}
            value={exposure}
            disabled={!canChangeExposure}
            aria-label={`Exposure for ${ruleName}`}
            onChange={(e) => setExposure(Number(e.target.value))}
            className="flex-1 accent-[var(--color-brand)]"
          />
          <Input
            id={`exposure-${ruleName}`}
            type="number"
            min={rerandomize ? exposureStep(total) : current}
            max={100}
            step={exposureStep(total)}
            value={exposure}
            disabled={!canChangeExposure}
            onChange={(e) => setExposure(Number(e.target.value))}
            className="h-8 w-24 font-mono text-[12.5px]"
          />
        </div>
        <p className="mt-1 text-[11.5px] text-ink-muted">
          {summary.rampable
            ? `Exposure can only grow from ${current}%, so everyone already exposed keeps their arm.`
            : 'This experiment shares one salt between exposure and arms, so exposure cannot be ramped safely. Re-randomize to split them.'}
        </p>
        {canEditRules && (
          <label className="mt-1.5 flex items-center gap-2 text-[12px] text-ink-soft">
            <input
              type="checkbox"
              checked={rerandomize}
              onChange={(e) => {
                setRerandomize(e.target.checked)
                setDraft(current)
              }}
            />
            Re-randomize: new salts, every subject is reassigned
          </label>
        )}
        {intent !== 'none' && (
          <Button size="sm" className="mt-2" onClick={review}>
            {intent === 'grow' ? `Review ramp to ${exposure}%` : 'Review re-randomization'}
          </Button>
        )}
      </div>

      <table className="w-full text-[12.5px]">
        <thead>
          <tr className="text-left text-[11px] uppercase tracking-wide text-ink-muted">
            <th className="font-medium">Arm</th>
            <th className="text-right font-medium">of exposed</th>
            <th className="text-right font-medium">of matching</th>
          </tr>
        </thead>
        <tbody>
          {summary.arms.map((arm, i) => (
            <tr key={`${arm.variation}-${i}`} className="border-t">
              <td className="py-1 font-mono">{arm.variation}</td>
              <td className="py-1 text-right font-mono">{formatPercent(arm.ofExposed)}</td>
              <td className="py-1 text-right font-mono">{formatPercent(arm.ofAll)}</td>
            </tr>
          ))}
        </tbody>
      </table>

      {inPreview && (
        <p className="rounded-md bg-surface px-2.5 py-1.5 text-[12px]">
          Preview: <strong className="font-mono">{preview.variation}</strong> ·{' '}
          {describeReason(preview.reason)}
          {preview.doLog ? ' · logged' : ' · not logged'}
        </p>
      )}
    </section>
  )
}

export function StartExperiment({
  ruleName,
  variations,
  onReview,
}: {
  ruleName: string
  variations: string[]
  onReview: (edit: ExperimentEdit) => void
}) {
  const [open, setOpen] = useState(false)
  const [weights, setWeights] = useState<Record<string, number>>(() =>
    Object.fromEntries(variations.map((v, i) => [v, i < 2 ? 50 : 0])),
  )
  const [exposure, setExposure] = useState(10)
  const arms = armsFromWeights(weights)

  if (!open) {
    return (
      <Button size="sm" variant="ghost" className="mb-2" onClick={() => setOpen(true)}>
        <FlaskConical className="h-3.5 w-3.5" />
        Start an experiment
      </Button>
    )
  }

  return (
    <div className="mb-3 space-y-2 rounded-lg border border-dashed p-3">
      <p className="text-[12px] text-ink-muted">
        Arm weights are relative. Subjects this rule matches but does not expose continue to the
        next rule. Salts are generated when you save.
      </p>
      {variations.map((v) => (
        <div key={v} className="flex items-center gap-2">
          <span className="w-24 shrink-0 font-mono text-[12.5px] text-ink-soft">{v}</span>
          <Input
            type="number"
            min={0}
            value={weights[v] ?? 0}
            aria-label={`${v} weight`}
            onChange={(e) =>
              setWeights((prev) => ({ ...prev, [v]: Math.max(0, Number(e.target.value) || 0) }))
            }
            className="h-8 w-24 font-mono text-[12.5px]"
          />
        </div>
      ))}
      <div className="flex items-center gap-2">
        <span className="w-24 shrink-0 text-[12.5px] text-ink-soft">Exposure %</span>
        <Input
          type="number"
          min={0.01}
          max={100}
          step={0.01}
          value={exposure}
          aria-label={`Exposure for the new experiment on ${ruleName}`}
          onChange={(e) => setExposure(Number(e.target.value))}
          className="h-8 w-24 font-mono text-[12.5px]"
        />
      </div>
      <div className="flex gap-2">
        <Button
          size="sm"
          disabled={arms.length === 0 || !(exposure > 0 && exposure <= 100)}
          onClick={() => onReview({ op: 'create', ruleName, exposurePercent: exposure, arms })}
        >
          Review experiment
        </Button>
        <Button size="sm" variant="outline" onClick={() => setOpen(false)}>
          Cancel
        </Button>
      </div>
    </div>
  )
}

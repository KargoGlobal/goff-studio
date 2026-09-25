import { useEffect, useState } from 'react'
import { Calculator } from 'lucide-react'
import { usePower } from '@/hooks/useExperiments'
import { Card, Input, Spinner } from '@/components/ui/primitives'
import { daysBetween, defaultVariance, formatValue } from '@/lib/experiments'
import type { Experiment, Metric, PowerRequest } from '@/lib/experimentTypes'

function useDebounced<T>(value: T, ms: number): T {
  const [v, setV] = useState(value)
  useEffect(() => {
    const t = setTimeout(() => setV(value), ms)
    return () => clearTimeout(t)
  }, [value, ms])
  return v
}

function NumberField({
  label,
  value,
  onChange,
  step,
  suffix,
  disabled,
}: {
  label: string
  value: number
  onChange: (n: number) => void
  step?: number
  suffix?: string
  disabled?: boolean
}) {
  return (
    <label className="block text-[12px] text-ink-soft">
      {label}
      <div className="mt-1 flex items-center gap-1.5">
        <Input
          type="number"
          value={Number.isFinite(value) ? value : ''}
          step={step ?? 'any'}
          disabled={disabled}
          onChange={(e) => onChange(e.target.value === '' ? Number.NaN : Number(e.target.value))}
          className="h-8 font-mono text-[13px]"
        />
        {suffix && <span className="text-ink-muted">{suffix}</span>}
      </div>
    </label>
  )
}

export function PowerPanel({ experiment, catalog }: { experiment: Experiment; catalog: Metric[] }) {
  const primary = catalog.find((m) => m.key === experiment.metrics.primary[0])
  const [typedBaseline, setBaseline] = useState<number | null>(null)
  const baseline = typedBaseline ?? (primary?.format === 'percent' ? 0.3 : 1)
  const [variance, setVariance] = useState<number | null>(null)
  const [perDay, setPerDay] = useState(100_000)
  const [rho2, setRho2] = useState(0.3)
  const [target, setTarget] = useState(1)

  const days = Math.max(1, daysBetween(experiment.start, experiment.end) || 28)
  const arms = Math.max(2, experiment.variants.length)
  const effectiveVariance = variance ?? defaultVariance(primary?.format ?? 'number', baseline)

  const { alpha, power, cuped } = experiment.analysis
  const r: PowerRequest = {
    baseline_mean: baseline,
    variance: effectiveVariance,
    n_per_day: perDay,
    arms,
    alpha,
    power,
    cuped_rho2: cuped ? rho2 : 0,
    days,
    target_mde: target / 100,
  }
  const usable = Object.values(r).every((v) => Number.isFinite(v)) && baseline !== 0 && perDay > 0 && effectiveVariance > 0

  // Debounce the serialized request so typing does not fire a call per keystroke.
  const debounced = useDebounced(usable ? JSON.stringify(r) : null, 300)
  const { data, error, isFetching } = usePower(debounced ? (JSON.parse(debounced) as PowerRequest) : null)

  return (
    <Card className="p-4">
      <h3 className="flex items-center gap-2 text-[14px] font-semibold">
        <Calculator className="h-4 w-4 text-brand" aria-hidden /> MDE and duration
        {isFetching && <Spinner className="h-3 w-3" />}
      </h3>
      <p className="mt-1 text-[12px] text-ink-muted">
        {primary ? (
          <>
            For <strong className="text-ink-soft">{primary.name}</strong>, {arms} arms, alpha {experiment.analysis.alpha}, power{' '}
            {experiment.analysis.power}.
          </>
        ) : (
          'Pick a primary metric to size the experiment.'
        )}
      </p>
      <div className="mt-3 grid grid-cols-2 gap-3">
        <NumberField
          label={`Baseline mean${primary ? ` (${formatValue(baseline, primary.format)})` : ''}`}
          value={baseline}
          onChange={setBaseline}
        />
        <NumberField label="Variance per unit" value={effectiveVariance} onChange={(n) => setVariance(n)} />
        <NumberField label="Units per day (all arms)" value={perDay} onChange={setPerDay} step={1000} />
        <NumberField label="Target MDE" value={target} onChange={setTarget} step={0.1} suffix="%" />
        <NumberField
          label="CUPED variance reduction"
          value={rho2}
          onChange={setRho2}
          step={0.05}
          disabled={!experiment.analysis.cuped}
        />
      </div>
      <div className="mt-4 rounded-lg bg-canvas p-3 text-[13px]" aria-live="polite">
        {error ? (
          <p className="text-danger">{error.message}</p>
        ) : data ? (
          <>
            <p>
              Detectable lift over the planned {data.days} days:{' '}
              <strong className="tabular-nums">±{(data.mde * 100).toFixed(2)}%</strong>
            </p>
            <p className="mt-1">
              Days to detect {target}%:{' '}
              <strong className="tabular-nums">{data.days_to_mde ?? '—'}</strong>
              {data.days_to_mde !== null && data.days_to_mde > 56 && (
                <span className="text-warn"> (longer than the 8-week limit)</span>
              )}
            </p>
            {data.source === 'local' && (
              <p className="mt-1 text-[11.5px] text-ink-muted">Estimated in Studio (two-sample z-test); no analysis service configured.</p>
            )}
          </>
        ) : (
          <p className="text-ink-muted">Fill in the inputs to estimate.</p>
        )}
      </div>
    </Card>
  )
}

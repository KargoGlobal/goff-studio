import { Fragment } from 'react'
import { CheckCircle2, XCircle } from 'lucide-react'
import { CIBar } from '@/components/experiments/CIBar'
import { Badge } from '@/components/ui/primitives'
import {
  ciDomain,
  cupedMode,
  formatCI,
  formatLift,
  formatPValue,
  formatValue,
  liftTone,
  UNDEFINED_LIFT,
  type Tone,
} from '@/lib/experiments'
import type { MetricFormat, MetricResult, Role } from '@/lib/experimentTypes'
import { cn } from '@/lib/cn'

function Interval({
  low,
  high,
  lift,
  domain,
  tone,
  label,
  note,
}: {
  low: number | null
  high: number | null
  lift: number | null
  domain: number
  tone: Tone
  label: string
  note?: string
}) {
  if (low == null || high == null || lift == null) {
    return (
      <span className="text-ink-muted" title={UNDEFINED_LIFT}>
        —
      </span>
    )
  }
  return (
    <div className="flex items-center gap-2">
      <CIBar low={low} high={high} lift={lift} domain={domain} tone={tone} width={120} label={label} />
      <span className="whitespace-nowrap text-[11.5px] tabular-nums text-ink-muted">{note ?? formatCI(low, high)}</span>
    </div>
  )
}

function Values({ control, value, format }: { control: number; value: number; format: MetricFormat }) {
  return (
    <>
      <td className="px-3 py-2.5 text-right tabular-nums text-ink-soft">{formatValue(control, format)}</td>
      <td className="px-3 py-2.5 text-right tabular-nums">{formatValue(value, format)}</td>
    </>
  )
}

const ROLE_TITLE: Record<Role, string> = {
  primary: 'Primary metrics',
  secondary: 'Secondary metrics',
  guardrail: 'Guardrails',
}

export function MetricsTable({
  role,
  metrics,
  level,
  showCuped,
}: {
  role: Role
  metrics: MetricResult[]
  level: string
  showCuped: boolean
}) {
  if (metrics.length === 0) return null
  const mode = cupedMode(metrics)
  // New documents are already adjusted at the top level; the extra column then shows the unadjusted readout.
  const cuped = mode === 'adjusted' || (showCuped && mode === 'legacy')
  const guard = role === 'guardrail'

  return (
    <section aria-label={ROLE_TITLE[role]}>
      <h3 className="mb-2 text-[13px] font-semibold uppercase tracking-wide text-ink-muted">{ROLE_TITLE[role]}</h3>
      <div className="overflow-x-auto rounded-lg border">
        <table className="w-full min-w-[860px] text-[13px]">
          <thead className="bg-canvas">
            <tr className="border-b text-left text-[11px] font-medium uppercase tracking-wide whitespace-nowrap text-ink-muted">
              <th className="px-3 py-2">Metric</th>
              <th className="px-3 py-2">Variant</th>
              <th className="px-3 py-2 text-right">Control</th>
              <th className="px-3 py-2 text-right">Variant</th>
              <th className="px-3 py-2 text-right">Lift</th>
              <th className="px-3 py-2">{level} interval</th>
              <th className="px-3 py-2 text-right">p-value</th>
              {cuped && <th className="px-3 py-2">{mode === 'adjusted' ? 'Unadjusted' : 'CUPED-adjusted'}</th>}
              <th className="px-3 py-2">{guard ? 'Guardrail' : 'Result'}</th>
            </tr>
          </thead>
          <tbody>
            {metrics.map((m) => {
              const domain = ciDomain(
                m.results.flatMap((r) => [
                  r,
                  ...(r.cuped ? [r.cuped] : []),
                  ...(r.raw ? [r.raw] : []),
                ]),
              )
              return (
                <Fragment key={m.key}>
                  {m.results.map((r, i) => {
                    const tone = liftTone(m.direction, r)
                    return (
                      <tr key={r.variant} className={cn('border-b last:border-0', i > 0 && 'border-t-0')}>
                        {i === 0 && (
                          <td rowSpan={m.results.length} className="min-w-[12rem] px-3 py-2.5 align-top">
                            <p className="font-medium text-ink">{m.name}</p>
                            <p className="font-mono text-[11px] text-ink-muted">
                              {m.key} · {m.direction === 'decrease' ? 'lower is better' : 'higher is better'}
                            </p>
                          </td>
                        )}
                        <td className="px-3 py-2.5 font-mono text-[12.5px]">{r.variant}</td>
                        <Values control={r.control_value} value={r.value} format={m.format} />
                        <td
                          className={cn(
                            'whitespace-nowrap px-3 py-2.5 text-right font-medium tabular-nums',
                            tone === 'good' && 'text-ok',
                            tone === 'bad' && 'text-danger',
                          )}
                          title={r.lift == null ? UNDEFINED_LIFT : undefined}
                        >
                          {r.lift == null ? <span className="font-normal text-ink-muted">n/a</span> : formatLift(r.lift)}
                          {r.raw && (
                            <Badge tone="brand" className="ml-1.5" title="CUPED-adjusted estimate">
                              CUPED
                            </Badge>
                          )}
                        </td>
                        <td className="px-3 py-2.5">
                          <Interval low={r.ci_low} high={r.ci_high} lift={r.lift} domain={domain} tone={tone} label={`${m.name}, ${r.variant}`} />
                        </td>
                        <td
                          className="px-3 py-2.5 text-right tabular-nums text-ink-soft"
                          title={r.adjusted_p != null && r.adjusted_p !== r.p_value ? `unadjusted for multiple comparisons: p = ${formatPValue(r.p_value)}` : undefined}
                        >
                          {formatPValue(r.adjusted_p ?? r.p_value)}
                        </td>
                        {cuped && (
                          <td className="px-3 py-2.5">
                            {mode === 'adjusted' && r.raw ? (
                              <Interval
                                low={r.raw.ci_low}
                                high={r.raw.ci_high}
                                lift={r.raw.lift}
                                domain={domain}
                                tone="neutral"
                                label={`${m.name}, ${r.variant}, unadjusted`}
                                note={`${formatLift(r.raw.lift)} · p ${formatPValue(r.raw.p_value)}${r.cuped ? ` · VR ${Math.round(r.cuped.variance_reduction * 100)}%` : ''}`}
                              />
                            ) : mode === 'legacy' && r.cuped ? (
                              <Interval
                                low={r.cuped.ci_low}
                                high={r.cuped.ci_high}
                                lift={r.cuped.lift}
                                domain={domain}
                                tone={liftTone(m.direction, {
                                  lift: r.cuped.lift,
                                  significant: r.cuped.ci_low != null && r.cuped.ci_high != null && (r.cuped.ci_low > 0 || r.cuped.ci_high < 0),
                                })}
                                label={`${m.name}, ${r.variant}, CUPED`}
                                note={`${formatLift(r.cuped.lift)} · VR ${Math.round(r.cuped.variance_reduction * 100)}%`}
                              />
                            ) : (
                              <span className="text-ink-muted">—</span>
                            )}
                          </td>
                        )}
                        <td className="whitespace-nowrap px-3 py-2.5">
                          {guard && r.guardrail?.reason ? (
                            <Badge tone="warn" title={r.guardrail.reason}>
                              no data
                            </Badge>
                          ) : guard && r.guardrail ? (
                            r.guardrail.pass ? (
                              <Badge tone="ok" className="gap-1" title={`No drop beyond ${r.guardrail.max_drop_pct}% can be ruled in`}>
                                <CheckCircle2 className="h-3 w-3" aria-hidden /> pass
                              </Badge>
                            ) : (
                              <Badge tone="danger" className="gap-1" title={`A drop beyond ${r.guardrail.max_drop_pct}% cannot be ruled out`}>
                                <XCircle className="h-3 w-3" aria-hidden /> fail
                              </Badge>
                            )
                          ) : r.significant ? (
                            <Badge tone={tone === 'good' ? 'ok' : 'danger'}>significant</Badge>
                          ) : (
                            <Badge tone="neutral">not significant</Badge>
                          )}
                          {guard && r.guardrail?.reason && (
                            <span className="ml-1.5 text-[11px] text-ink-muted">{r.guardrail.reason}</span>
                          )}
                          {guard && r.guardrail?.max_drop_pct != null && (
                            <span className="ml-1.5 text-[11px] text-ink-muted">max drop {r.guardrail.max_drop_pct}%</span>
                          )}
                        </td>
                      </tr>
                    )
                  })}
                </Fragment>
              )
            })}
          </tbody>
        </table>
      </div>
    </section>
  )
}

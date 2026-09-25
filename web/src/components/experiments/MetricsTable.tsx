import { Fragment } from 'react'
import { CheckCircle2, XCircle } from 'lucide-react'
import { CIBar } from '@/components/experiments/CIBar'
import { Badge } from '@/components/ui/primitives'
import {
  ciDomain,
  formatCI,
  formatLift,
  formatPValue,
  formatValue,
  liftTone,
} from '@/lib/experiments'
import type { MetricResult, Role } from '@/lib/experimentTypes'
import { cn } from '@/lib/cn'

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
  const cuped = showCuped && metrics.some((m) => m.results.some((r) => r.cuped))
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
              {cuped && <th className="px-3 py-2">CUPED-adjusted</th>}
              <th className="px-3 py-2">{guard ? 'Guardrail' : 'Result'}</th>
            </tr>
          </thead>
          <tbody>
            {metrics.map((m) => {
              const domain = ciDomain(
                m.results.flatMap((r) => (r.cuped ? [r, { ci_low: r.cuped.ci_low, ci_high: r.cuped.ci_high }] : [r])),
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
                        <td className="px-3 py-2.5 text-right tabular-nums text-ink-soft">{formatValue(r.control_value, m.format)}</td>
                        <td className="px-3 py-2.5 text-right tabular-nums">{formatValue(r.value, m.format)}</td>
                        <td
                          className={cn(
                            'px-3 py-2.5 text-right font-medium tabular-nums',
                            tone === 'good' && 'text-ok',
                            tone === 'bad' && 'text-danger',
                          )}
                        >
                          {formatLift(r.lift)}
                        </td>
                        <td className="px-3 py-2.5">
                          <div className="flex items-center gap-2">
                            <CIBar low={r.ci_low} high={r.ci_high} lift={r.lift} domain={domain} tone={tone} width={120} label={`${m.name}, ${r.variant}`} />
                            <span className="whitespace-nowrap text-[11.5px] tabular-nums text-ink-muted">{formatCI(r.ci_low, r.ci_high)}</span>
                          </div>
                        </td>
                        <td className="px-3 py-2.5 text-right tabular-nums text-ink-soft" title={r.adjusted_p !== r.p_value ? `raw p = ${formatPValue(r.p_value)}` : undefined}>
                          {formatPValue(r.adjusted_p ?? r.p_value)}
                        </td>
                        {cuped && (
                          <td className="px-3 py-2.5">
                            {r.cuped ? (
                              <div className="flex items-center gap-2">
                                <CIBar
                                  low={r.cuped.ci_low}
                                  high={r.cuped.ci_high}
                                  lift={r.cuped.lift}
                                  domain={domain}
                                  tone={liftTone(m.direction, { lift: r.cuped.lift, significant: r.cuped.ci_low > 0 || r.cuped.ci_high < 0 })}
                                  width={120}
                                  label={`${m.name}, ${r.variant}, CUPED`}
                                />
                                <span className="whitespace-nowrap text-[11.5px] tabular-nums text-ink-muted">
                                  {formatLift(r.cuped.lift)} · VR {Math.round(r.cuped.variance_reduction * 100)}%
                                </span>
                              </div>
                            ) : (
                              <span className="text-ink-muted">—</span>
                            )}
                          </td>
                        )}
                        <td className="whitespace-nowrap px-3 py-2.5">
                          {guard && r.guardrail ? (
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
                          {guard && r.guardrail && (
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

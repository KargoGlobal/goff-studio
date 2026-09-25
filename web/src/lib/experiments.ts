import type {
  ArmStat,
  Direction,
  Experiment,
  ExperimentStatus,
  MetricFormat,
  MetricResult,
  Recommendation,
  Results,
  Role,
} from '@/lib/experimentTypes'

const DAY = 24 * 60 * 60 * 1000

export function formatValue(value: number, format: MetricFormat): string {
  if (!Number.isFinite(value)) return '—'
  switch (format) {
    case 'percent':
      return `${(value * 100).toFixed(Math.abs(value) < 0.01 ? 2 : 1)}%`
    case 'currency':
      return `$${value.toFixed(Math.abs(value) < 10 ? 3 : 2)}`
    default:
      return Math.abs(value) >= 1000
        ? Math.round(value).toLocaleString('en-US')
        : Number(value.toPrecision(4)).toString()
  }
}

export function formatLift(lift: number | undefined | null): string {
  if (lift === undefined || lift === null || !Number.isFinite(lift)) return '—'
  const pct = lift * 100
  const digits = Math.abs(pct) < 10 ? 2 : 1
  const text = pct.toFixed(digits)
  if (Number(text) === 0) return `${(0).toFixed(digits)}%`
  return `${pct > 0 ? '+' : ''}${text}%`
}

export function formatCI(low: number | null | undefined, high: number | null | undefined): string {
  if (low == null || high == null) return '—'
  return `[${formatLift(low)}, ${formatLift(high)}]`
}

export function formatPValue(p: number | null | undefined): string {
  if (p == null || !Number.isFinite(p)) return '—'
  if (p < 0.001) return '<0.001'
  return p.toFixed(3)
}

export function confidenceLevel(alpha: number): string {
  return `${Math.round((1 - alpha) * 1000) / 10}%`
}

export type Tone = 'good' | 'bad' | 'neutral'

// Tone follows the metric's goal, so a significant drop in a "decrease" metric is good.
export function liftTone(direction: Direction, arm: Pick<ArmStat, 'lift' | 'significant'>): Tone {
  if (!arm.significant || arm.lift == null) return 'neutral'
  const up = arm.lift > 0
  return (direction === 'decrease' ? !up : up) ? 'good' : 'bad'
}

// A symmetric domain around zero so every CI bar in a table shares one scale.
export function ciDomain(arms: { ci_low: number | null; ci_high: number | null }[]): number {
  let max = 0
  for (const a of arms) {
    if (a.ci_low == null || a.ci_high == null) continue
    max = Math.max(max, Math.abs(a.ci_low), Math.abs(a.ci_high))
  }
  if (!Number.isFinite(max) || max === 0) return 0.01
  return max * 1.15
}

export interface Geometry {
  zero: number
  low: number
  high: number
  point: number
}

export function ciGeometry(low: number, high: number, point: number, domain: number, width: number): Geometry {
  const d = domain > 0 ? domain : 0.01
  const x = (v: number) => {
    const clamped = Math.max(-d, Math.min(d, v))
    return ((clamped + d) / (2 * d)) * width
  }
  return { zero: x(0), low: x(low), high: x(high), point: x(point) }
}

export function groupByRole(metrics: MetricResult[]): Record<Role, MetricResult[]> {
  const out: Record<Role, MetricResult[]> = { primary: [], secondary: [], guardrail: [] }
  for (const m of metrics) out[m.role]?.push(m)
  return out
}

export const RECOMMENDATION_LABEL: Record<Recommendation, string> = {
  roll_out: 'Roll out',
  discuss: 'Discuss',
  do_not_roll_out: 'Do not roll out',
  keep_running: 'Keep running',
}

export const DECISION_LABEL: Record<string, string> = {
  roll_out: 'Rolled out',
  do_not_roll_out: 'Not rolled out',
  extend: 'Extended',
}

export function recommendationTone(r: Recommendation | undefined): 'ok' | 'warn' | 'danger' | 'neutral' {
  switch (r) {
    case 'roll_out':
      return 'ok'
    case 'discuss':
      return 'warn'
    case 'do_not_roll_out':
      return 'danger'
    default:
      return 'neutral'
  }
}

export function statusTone(s: ExperimentStatus): 'brand' | 'neutral' | 'warn' | 'ok' {
  switch (s) {
    case 'running':
      return 'brand'
    case 'stopped':
      return 'warn'
    case 'concluded':
      return 'ok'
    default:
      return 'neutral'
  }
}

export function daysBetween(start: string, end: string): number {
  return Math.round((Date.parse(end) - Date.parse(start)) / DAY)
}

export function formatDate(iso: string): string {
  if (!iso) return '—'
  return iso.slice(0, 10)
}

export function describeTiming(e: { status: ExperimentStatus; daysRunning: number; daysRemaining: number }): string {
  if (e.status === 'draft') return 'not started'
  if (e.daysRunning === 0 && e.daysRemaining > 0) return `starts soon · ${e.daysRemaining}d planned`
  if (e.daysRemaining === 0) return `${e.daysRunning}d, ended`
  return `${e.daysRunning}d run · ${e.daysRemaining}d left`
}

export interface SeriesPoint {
  date: string
  lift: number
  low: number
  high: number
}

export function liftSeries(results: Results, metric: string): Map<string, SeriesPoint[]> {
  const out = new Map<string, SeriesPoint[]>()
  for (const p of results.timeseries ?? []) {
    if (p.metric !== metric || p.lift == null || p.ci_low == null || p.ci_high == null) continue
    const list = out.get(p.variant) ?? []
    list.push({ date: p.date, lift: p.lift, low: p.ci_low, high: p.ci_high })
    out.set(p.variant, list)
  }
  for (const list of out.values()) list.sort((a, b) => a.date.localeCompare(b.date))
  return out
}

// Variance guess for the power calculator: exact for rates, CV of 1 otherwise.
export function defaultVariance(format: MetricFormat, baseline: number): number {
  if (format === 'percent' && baseline > 0 && baseline < 1) return baseline * (1 - baseline)
  return baseline * baseline
}

/** Why a lift cannot be shown: relative lift is undefined when the control mean is not positive. */
export const UNDEFINED_LIFT = 'Relative lift is undefined because the control mean is zero or negative'

/**
 * How CUPED shows up for an arm. New documents put the adjusted estimate at the
 * top level and the unadjusted one in `raw`; older ones (no `raw`) keep the
 * unadjusted estimate at the top level and the adjusted one in `cuped`.
 */
export function cupedMode(metrics: MetricResult[]): 'adjusted' | 'legacy' | 'none' {
  const arms = metrics.flatMap((m) => m.results)
  if (arms.some((a) => a.raw)) return 'adjusted'
  if (arms.some((a) => a.cuped)) return 'legacy'
  return 'none'
}

export function describeGuardrail(g: NonNullable<ArmStat['guardrail']>): string {
  if (g.reason) return `no data (${g.reason})`
  return g.pass ? 'pass' : 'FAIL'
}

export function guardrailPasses(m: MetricResult): boolean {
  return m.results.every((r) => r.guardrail?.pass !== false)
}

function cell(text: string): string {
  return text.replace(/\|/g, '\\|').replace(/\n/g, ' ')
}

function table(header: string[], rows: string[][]): string {
  const lines = [
    `| ${header.join(' | ')} |`,
    `| ${header.map(() => '---').join(' | ')} |`,
    ...rows.map((r) => `| ${r.map(cell).join(' | ')} |`),
  ]
  return lines.join('\n')
}

export function readoutMarkdown(e: Experiment, r: Results): string {
  const level = confidenceLevel(r.method?.alpha ?? e.analysis.alpha)
  const groups = groupByRole(r.metrics ?? [])
  const out: string[] = []

  out.push(`# Experiment readout: ${e.name}`)
  out.push('')
  if (r.sample) out.push('> **Sample data.** No analysis service is configured; these numbers are generated for illustration.\n')
  out.push(`- **Key:** \`${e.key}\``)
  out.push(`- **Hypothesis:** ${e.hypothesis}`)
  out.push(`- **Owner:** ${e.owner}${e.ticket ? ` · **Ticket:** ${e.ticket}` : ''}`)
  out.push(`- **Flag:** \`${e.flag}\` in ${e.environment} · **Arms:** ${e.variants.join(', ')} (control: ${e.control})`)
  out.push(`- **Window:** ${formatDate(e.start)} to ${formatDate(e.end)} · **Unit:** ${e.unit.type}`)
  out.push(
    `- **Method:** ${r.method?.test ?? e.analysis.test}, alpha ${r.method?.alpha ?? e.analysis.alpha}` +
      `${(r.method?.cuped ?? e.analysis.cuped) ? ', CUPED' : ''}, correction ${r.method?.correction ?? e.analysis.correction}`,
  )
  out.push(`- **Data as of:** ${r.as_of}`)
  out.push('')

  out.push('## Decision')
  out.push('')
  if (r.decision) {
    const target = r.decision.variant ? ` ${r.decision.variant}` : ''
    out.push(`**Recommendation: ${RECOMMENDATION_LABEL[r.decision.recommendation] ?? r.decision.recommendation}${target}.** ${r.decision.reason}`)
  } else {
    out.push('No recommendation yet.')
  }
  if (e.decision) {
    out.push('')
    out.push(
      `**Recorded decision:** ${DECISION_LABEL[e.decision.outcome] ?? e.decision.outcome}` +
        `${e.decision.variant ? ` (${e.decision.variant})` : ''}` +
        `${e.decision.by ? ` by ${e.decision.by}` : ''}${e.decision.at ? ` on ${formatDate(e.decision.at)}` : ''}` +
        `${e.decision.note ? `. ${e.decision.note}` : ''}`,
    )
  }
  out.push('')

  const adjusted = cupedMode(r.metrics ?? []) === 'adjusted'
  const liftCell = (a: ArmStat) =>
    a.lift == null ? 'n/a (control mean <= 0)' : `${formatLift(a.lift)}${a.raw ? ' (CUPED)' : ''}`
  const rowsFor = (ms: MetricResult[]) =>
    ms.flatMap((m) =>
      m.results.map((a) => [
        m.name,
        a.variant,
        formatValue(a.control_value, m.format),
        formatValue(a.value, m.format),
        liftCell(a),
        formatCI(a.ci_low, a.ci_high),
        formatPValue(a.adjusted_p ?? a.p_value),
        a.significant ? 'yes' : 'no',
        ...(adjusted ? [a.raw ? `${formatLift(a.raw.lift)} ${formatCI(a.raw.ci_low, a.raw.ci_high)}, p ${formatPValue(a.raw.p_value)}` : '—'] : []),
      ]),
    )
  const header = [
    'Metric',
    'Variant',
    'Control',
    'Variant value',
    'Lift',
    `${level} CI`,
    'p-value',
    'Significant',
    ...(adjusted ? ['Unadjusted (no CUPED)'] : []),
  ]

  if (adjusted) {
    out.push('Lifts, intervals and p-values are CUPED-adjusted; the unadjusted readout is shown alongside.')
    out.push('')
  }
  out.push('## Primary metrics')
  out.push('')
  out.push(groups.primary.length ? table(header, rowsFor(groups.primary)) : 'None.')
  out.push('')

  out.push('## Guardrails')
  out.push('')
  if (groups.guardrail.length) {
    out.push(
      table(
        ['Metric', 'Variant', 'Lift', `${level} CI`, 'Max drop', 'Result'],
        groups.guardrail.flatMap((m) =>
          m.results.map((a) => [
            m.name,
            a.variant,
            liftCell(a),
            formatCI(a.ci_low, a.ci_high),
            a.guardrail?.max_drop_pct != null ? `${a.guardrail.max_drop_pct}%` : '—',
            a.guardrail ? describeGuardrail(a.guardrail) : '—',
          ]),
        ),
      ),
    )
  } else {
    out.push('None.')
  }
  out.push('')

  if (groups.secondary.length) {
    out.push('## Secondary metrics')
    out.push('')
    out.push(table(header, rowsFor(groups.secondary)))
    out.push('')
  }

  out.push('## Sample ratio')
  out.push('')
  out.push(
    r.srm?.flag
      ? `**Sample ratio mismatch** (p = ${formatPValue(r.srm.p_value)}). Results are not trustworthy until the cause is found.`
      : `Traffic matches the planned split (p = ${formatPValue(r.srm?.p_value ?? 1)}).`,
  )
  out.push('')
  return out.join('\n')
}

export const MAX_WEEKS = 8

export function blankExperiment(today = new Date()): Experiment {
  const start = new Date(Date.UTC(today.getUTCFullYear(), today.getUTCMonth(), today.getUTCDate() + 1))
  const end = new Date(start.getTime() + 28 * DAY)
  return {
    key: '',
    name: '',
    owner: '',
    hypothesis: '',
    ticket: '',
    flag: '',
    environment: '',
    allocations: [],
    control: '',
    variants: [],
    unit: { type: 'request', key: 'targetingKey' },
    start: start.toISOString().replace(/\.\d{3}Z$/, 'Z'),
    end: end.toISOString().replace(/\.\d{3}Z$/, 'Z'),
    status: 'draft',
    metrics: { primary: [], secondary: [], guardrails: [] },
    analysis: { test: 'sequential', alpha: 0.05, power: 0.8, cuped: false, covariate: '', correction: 'none', strata: [] },
    segments: [],
    decision: null,
  }
}

// Client-side checks mirror the server's so most mistakes surface before review.
export function formProblems(e: Experiment): string[] {
  const out: string[] = []
  if (!/^[a-z0-9][a-z0-9.-]*$/.test(e.key)) out.push('Key: use lowercase letters, digits, dots and dashes.')
  if (!e.name.trim()) out.push('Give the experiment a name.')
  if (!e.owner.trim()) out.push('Pick the owning team.')
  if (!e.hypothesis.trim()) out.push('Write down the hypothesis.')
  if (!e.flag) out.push('Pick a flag.')
  if (e.allocations.length === 0) out.push('Pick at least one rule on the flag.')
  if (e.variants.length < 2) out.push('Pick a control and at least one other variant.')
  if (!e.variants.includes(e.control)) out.push('The control must be one of the chosen variants.')
  const start = Date.parse(e.start)
  const end = Date.parse(e.end)
  if (Number.isNaN(start) || Number.isNaN(end)) out.push('Set both a start and an end date.')
  else if (end <= start) out.push('The end date must be after the start date.')
  else if (end - start > MAX_WEEKS * 7 * DAY && !e.extended)
    out.push(`Experiments run for at most ${MAX_WEEKS} weeks unless marked extended.`)
  if (e.metrics.primary.length === 0) out.push('Pick at least one primary metric.')
  for (const g of e.metrics.guardrails ?? []) {
    if (!g.metric) out.push('A guardrail has no metric.')
    else if (!(g.max_drop_pct > 0 && g.max_drop_pct <= 100)) out.push(`Guardrail ${g.metric} needs a max drop between 0 and 100%.`)
  }
  const all = [...e.metrics.primary, ...(e.metrics.secondary ?? []), ...(e.metrics.guardrails ?? []).map((g) => g.metric)]
  const dupe = all.find((m, i) => m && all.indexOf(m) !== i)
  if (dupe) out.push(`Metric ${dupe} is used twice.`)
  if (!(e.analysis.alpha > 0 && e.analysis.alpha < 0.5)) out.push('Alpha must be between 0 and 0.5.')
  if (!(e.analysis.power > 0 && e.analysis.power < 1)) out.push('Power must be between 0 and 1.')
  return out
}

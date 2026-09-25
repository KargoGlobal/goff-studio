import { describe, expect, it } from 'vitest'
import {
  blankExperiment,
  ciDomain,
  ciGeometry,
  defaultVariance,
  describeTiming,
  formatCI,
  formatLift,
  formatPValue,
  formatValue,
  formProblems,
  groupByRole,
  liftSeries,
  liftTone,
  cupedMode,
  readoutMarkdown,
} from '@/lib/experiments'
import type { Experiment, MetricResult, Results } from '@/lib/experimentTypes'

describe('formatting', () => {
  it('formats values by metric format', () => {
    expect(formatValue(0.412, 'percent')).toBe('41.2%')
    expect(formatValue(0.0042, 'percent')).toBe('0.42%')
    expect(formatValue(1.2345, 'currency')).toBe('$1.234')
    expect(formatValue(12.5, 'currency')).toBe('$12.50')
    expect(formatValue(1234567.8, 'number')).toBe('1,234,568')
    expect(formatValue(3.14159, 'number')).toBe('3.142')
    expect(formatValue(Number.NaN, 'number')).toBe('—')
  })

  it('signs lifts and avoids negative zero', () => {
    expect(formatLift(0.0274)).toBe('+2.74%')
    expect(formatLift(-0.013)).toBe('-1.30%')
    expect(formatLift(0.1234)).toBe('+12.3%')
    expect(formatLift(-0.000001)).toBe('0.00%')
    expect(formatLift(undefined)).toBe('—')
    expect(formatCI(-0.01, 0.03)).toBe('[-1.00%, +3.00%]')
  })

  it('floors tiny p-values', () => {
    expect(formatPValue(0.0004)).toBe('<0.001')
    expect(formatPValue(0.0456)).toBe('0.046')
  })
})

describe('liftTone', () => {
  it('follows the metric direction', () => {
    expect(liftTone('increase', { lift: 0.02, significant: true })).toBe('good')
    expect(liftTone('increase', { lift: -0.02, significant: true })).toBe('bad')
    expect(liftTone('decrease', { lift: -0.02, significant: true })).toBe('good')
    expect(liftTone('decrease', { lift: 0.02, significant: false })).toBe('neutral')
  })
})

describe('CI bar geometry', () => {
  it('uses a symmetric domain around zero', () => {
    expect(ciDomain([{ ci_low: -0.01, ci_high: 0.04 }])).toBeCloseTo(0.046)
    expect(ciDomain([])).toBe(0.01)
  })

  it('places zero in the middle and clamps to the domain', () => {
    const g = ciGeometry(0.01, 0.03, 0.02, 0.04, 200)
    expect(g.zero).toBe(100)
    expect(g.low).toBeCloseTo(125)
    expect(g.point).toBeCloseTo(150)
    expect(g.high).toBeCloseTo(175)
    const clamped = ciGeometry(-1, 1, 0, 0.04, 200)
    expect(clamped.low).toBe(0)
    expect(clamped.high).toBe(200)
  })
})

describe('helpers', () => {
  it('estimates variance from the baseline', () => {
    expect(defaultVariance('percent', 0.4)).toBeCloseTo(0.24)
    expect(defaultVariance('currency', 2)).toBe(4)
  })

  it('describes timing', () => {
    expect(describeTiming({ status: 'draft', daysRunning: 0, daysRemaining: 10 })).toBe('not started')
    expect(describeTiming({ status: 'running', daysRunning: 10, daysRemaining: 18 })).toBe('10d run · 18d left')
    expect(describeTiming({ status: 'concluded', daysRunning: 28, daysRemaining: 0 })).toBe('28d, ended')
  })
})

const experiment: Experiment = {
  key: 'tmax-exp-us-east-1',
  name: 'TMAX US-East',
  owner: 'bidder',
  hypothesis: 'Lower tmax | raises bid rate',
  ticket: 'EXP-1',
  flag: 'tmax',
  environment: 'production',
  allocations: ['exp-us-east-1'],
  control: 'control',
  variants: ['control', 'tmax150'],
  unit: { type: 'request', key: 'targetingKey' },
  start: '2026-09-10T00:00:00Z',
  end: '2026-10-08T00:00:00Z',
  status: 'running',
  metrics: { primary: ['dsp_bid_rate'], secondary: [], guardrails: [{ metric: 'avg_bid_cpm', max_drop_pct: 2 }] },
  analysis: { test: 'sequential', alpha: 0.05, power: 0.8, cuped: false, correction: 'none', strata: [] },
  segments: [],
  decision: { outcome: 'roll_out', variant: 'tmax150', by: 'ada@acme.com', at: '2026-10-09T00:00:00Z' },
}

const arm = (lift: number, lo: number, hi: number, guard?: boolean) => ({
  variant: 'tmax150',
  value: 0.41,
  control_value: 0.4,
  lift,
  ci_low: lo,
  ci_high: hi,
  p_value: 0.0004,
  adjusted_p: 0.0004,
  significant: lo > 0 || hi < 0,
  cuped: null,
  guardrail: guard === undefined ? null : { max_drop_pct: 2, pass: guard },
})

const metrics: MetricResult[] = [
  { key: 'dsp_bid_rate', name: 'DSP bid rate', kind: 'mean', role: 'primary', direction: 'increase', format: 'percent', results: [arm(0.025, 0.01, 0.04)] },
  { key: 'avg_bid_cpm', name: 'Average bid CPM', kind: 'ratio', role: 'guardrail', direction: 'increase', format: 'currency', results: [arm(-0.03, -0.05, -0.01, false)] },
]

const results: Results = {
  experiment_key: experiment.key,
  as_of: '2026-09-20T12:00:00Z',
  status: 'ok',
  message: null,
  unit: 'request',
  method: { test: 'sequential', alpha: 0.05, cuped: false, correction: 'none' },
  variants: [],
  srm: { chi2: 0.4, p_value: 0.52, flag: false, max_abs_deviation: 0.001 },
  metrics,
  segments: [],
  timeseries: [
    { date: '2026-09-12', metric: 'dsp_bid_rate', variant: 'tmax150', lift: 0.02, ci_low: 0, ci_high: 0.04 },
    { date: '2026-09-11', metric: 'dsp_bid_rate', variant: 'tmax150', lift: 0.01, ci_low: -0.02, ci_high: 0.04 },
    { date: '2026-09-11', metric: 'other', variant: 'tmax150', lift: 0.5, ci_low: 0, ci_high: 1 },
  ],
  diagnostics: [],
  decision: { recommendation: 'do_not_roll_out', variant: 'tmax150', reason: 'Guardrail hurt.' },
  sample: true,
}

describe('groupByRole and liftSeries', () => {
  it('groups metrics by role', () => {
    const g = groupByRole(metrics)
    expect(g.primary.map((m) => m.key)).toEqual(['dsp_bid_rate'])
    expect(g.guardrail.map((m) => m.key)).toEqual(['avg_bid_cpm'])
    expect(g.secondary).toEqual([])
  })

  it('builds sorted per-variant series for one metric', () => {
    const s = liftSeries(results, 'dsp_bid_rate')
    expect([...s.keys()]).toEqual(['tmax150'])
    expect(s.get('tmax150')!.map((p) => p.date)).toEqual(['2026-09-11', '2026-09-12'])
  })
})

describe('readoutMarkdown', () => {
  const md = readoutMarkdown(experiment, results)

  it('leads with the hypothesis and flags sample data', () => {
    expect(md).toContain('# Experiment readout: TMAX US-East')
    expect(md).toContain('**Sample data.**')
    expect(md).toContain('**Hypothesis:** Lower tmax | raises bid rate')
  })

  it('includes the decision and the recorded decision', () => {
    expect(md).toContain('**Recommendation: Do not roll out tmax150.** Guardrail hurt.')
    expect(md).toContain('**Recorded decision:** Rolled out (tmax150) by ada@acme.com on 2026-10-09')
  })

  it('renders primary and guardrail tables', () => {
    expect(md).toContain('| Metric | Variant | Control | Variant value | Lift | 95% CI | p-value | Significant |')
    expect(md).toContain('| DSP bid rate | tmax150 | 40.0% | 41.0% | +2.50% | [+1.00%, +4.00%] | <0.001 | yes |')
    expect(md).toContain('| Average bid CPM | tmax150 | -3.00% | [-5.00%, -1.00%] | 2% | FAIL |')
    expect(md).not.toContain('## Secondary metrics')
    expect(md).toContain('Traffic matches the planned split (p = 0.520).')
  })

  it('escapes pipes inside table cells', () => {
    const weird = readoutMarkdown(experiment, {
      ...results,
      metrics: [{ ...metrics[0], name: 'a|b' }],
    })
    expect(weird).toContain('| a\\|b |')
  })
})


describe('CUPED and undefined estimates', () => {
  const adjustedArm = {
    ...arm(0.03, 0.015, 0.045),
    raw: { value: 0.412, control_value: 0.4, lift: 0.03, ci_low: 0.005, ci_high: 0.055, p_value: 0.02 },
    cuped: { value: 0.412, lift: 0.03, ci_low: 0.015, ci_high: 0.045, variance_reduction: 0.4 },
  }
  const legacyArm = {
    ...arm(0.03, 0.005, 0.055),
    cuped: { value: 0.412, lift: 0.03, ci_low: 0.015, ci_high: 0.045, variance_reduction: 0.4 },
  }
  const undefinedArm = { ...arm(0, 0, 0, false), lift: null, ci_low: null, ci_high: null, p_value: null, adjusted_p: null }
  const noData = {
    ...undefinedArm,
    guardrail: { pass: false, significant_harm: false, reason: 'no usable data' },
  }
  const doc = (ms: MetricResult[]): Results => ({ ...results, metrics: ms, sample: false })
  const primary = (a: MetricResult['results'][number]): MetricResult => ({ ...metrics[0], results: [a] })

  it('detects the adjusted, legacy and plain shapes', () => {
    expect(cupedMode([primary(adjustedArm)])).toBe('adjusted')
    expect(cupedMode([primary(legacyArm)])).toBe('legacy')
    expect(cupedMode(metrics)).toBe('none')
  })

  it('marks adjusted lifts as CUPED and shows the unadjusted readout alongside', () => {
    const md = readoutMarkdown(experiment, doc([primary(adjustedArm)]))
    expect(md).toContain('Lifts, intervals and p-values are CUPED-adjusted')
    expect(md).toContain('| Unadjusted (no CUPED) |')
    expect(md).toContain('| +3.00% (CUPED) | [+1.50%, +4.50%] |')
    expect(md).toContain('| +3.00% [+0.50%, +5.50%], p 0.020 |')
  })

  it('keeps the old table when raw is absent', () => {
    const md = readoutMarkdown(experiment, doc([primary(legacyArm)]))
    expect(md).not.toContain('Unadjusted')
    expect(md).not.toContain('(CUPED)')
  })

  it('renders undefined lifts and guardrails without data gracefully', () => {
    const md = readoutMarkdown(experiment, doc([primary(undefinedArm), { ...metrics[1], results: [noData] }]))
    expect(md).toContain('| n/a (control mean <= 0) | — | — | no |')
    expect(md).toContain('| no data (no usable data) |')
    expect(formatCI(null, 0.1)).toBe('—')
    expect(formatPValue(null)).toBe('—')
    expect(liftTone('increase', { lift: null, significant: true })).toBe('neutral')
    expect(ciDomain([{ ci_low: null, ci_high: null }, { ci_low: -0.1, ci_high: 0.2 }])).toBeCloseTo(0.23)
  })

  it('skips timeseries points without estimates', () => {
    const s = liftSeries(
      { ...results, timeseries: [{ date: '2026-09-11', metric: 'm', variant: 'b', lift: null, ci_low: null, ci_high: null }] },
      'm',
    )
    expect(s.size).toBe(0)
  })
})

describe('formProblems', () => {
  const ok = { ...experiment, decision: null }

  it('accepts a complete experiment', () => {
    expect(formProblems(ok)).toEqual([])
  })

  it('enforces the eight-week window unless extended', () => {
    const long = { ...ok, end: '2026-12-31T00:00:00Z' }
    expect(formProblems(long).join()).toContain('at most 8 weeks')
    expect(formProblems({ ...long, extended: true })).toEqual([])
  })

  it('catches missing arms, metrics and bad keys', () => {
    const bad = { ...ok, key: 'Bad Key', control: 'nope', metrics: { primary: [], secondary: [], guardrails: [{ metric: 'x', max_drop_pct: 0 }] } }
    const problems = formProblems(bad).join('\n')
    expect(problems).toContain('Key:')
    expect(problems).toContain('control must be one of')
    expect(problems).toContain('primary metric')
    expect(problems).toContain('max drop')
  })

  it('flags a metric used twice', () => {
    const dupe = { ...ok, metrics: { primary: ['a'], secondary: ['a'], guardrails: [] } }
    expect(formProblems(dupe).join()).toContain('Metric a is used twice')
  })

  it('starts a blank experiment tomorrow for four weeks', () => {
    const b = blankExperiment(new Date('2026-09-25T15:00:00Z'))
    expect(b.start).toBe('2026-09-26T00:00:00Z')
    expect(b.end).toBe('2026-10-24T00:00:00Z')
    expect(b.analysis.test).toBe('sequential')
  })
})

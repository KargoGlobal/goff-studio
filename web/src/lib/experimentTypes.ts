import type { Action, Broken } from '@/lib/api'

export type ExperimentStatus = 'draft' | 'running' | 'stopped' | 'concluded'
export type MetricFormat = 'percent' | 'currency' | 'number'
export type Direction = 'increase' | 'decrease'
export type Role = 'primary' | 'secondary' | 'guardrail'
export type Recommendation = 'roll_out' | 'discuss' | 'do_not_roll_out' | 'keep_running'

export interface Guardrail {
  metric: string
  max_drop_pct: number
}

export interface ExperimentDecision {
  outcome: 'roll_out' | 'do_not_roll_out' | 'extend'
  variant?: string
  note?: string
  by?: string
  at?: string
}

export interface Experiment {
  key: string
  name: string
  owner: string
  hypothesis: string
  ticket?: string
  flag: string
  environment: string
  allocations: string[]
  control: string
  variants: string[]
  unit: { type: 'request' | 'entity'; key: string }
  start: string
  end: string
  extended?: boolean
  status: ExperimentStatus
  metrics: { primary: string[]; secondary: string[] | null; guardrails: Guardrail[] | null }
  analysis: {
    test: 'sequential' | 'fixed'
    alpha: number
    power: number
    cuped: boolean
    covariate?: string
    correction: 'none' | 'holm' | 'bh'
    strata: string[] | null
  }
  segments: string[] | null
  decision: ExperimentDecision | null
}

export interface ResultSummary {
  status: string
  asOf: string
  srmFlag: boolean
  primaryMetric?: string
  primaryVariant?: string
  primaryLift?: number
  primarySignificant: boolean
  recommendation?: Recommendation
  sample: boolean
}

export interface ExperimentView extends Experiment {
  file: string
  flagFile: string
  fileSha: string
  actions: Action[]
  daysRunning: number
  daysRemaining: number
  results?: ResultSummary
}

export interface ExperimentList {
  experiments: ExperimentView[]
  broken: Broken[]
  sample: boolean
}

export interface Metric {
  key: string
  name: string
  kind: 'mean' | 'ratio'
  numerator: string
  denominator?: string
  format: MetricFormat
  direction: Direction
  cap?: { pct: number } | null
  description: string
}

export interface MetricView extends Metric {
  fileSha: string
  actions: Action[]
}

export interface MetricList {
  metrics: MetricView[]
  broken: Broken[]
  actions: Action[]
}

export interface CupedStat {
  value: number
  lift: number
  ci_low: number
  ci_high: number
  variance_reduction: number
}

export interface ArmStat {
  variant: string
  value: number
  control_value: number
  lift: number
  ci_low: number
  ci_high: number
  p_value: number
  adjusted_p: number
  significant: boolean
  cuped: CupedStat | null
  guardrail: { max_drop_pct: number; pass: boolean } | null
}

export interface MetricResult {
  key: string
  name: string
  kind: string
  role: Role
  direction: Direction
  format: MetricFormat
  results: ArmStat[]
}

export interface Results {
  experiment_key: string
  as_of: string
  status: 'ok' | 'insufficient_data' | 'error'
  message: string | null
  unit: string
  method: { test: string; alpha: number; cuped: boolean; correction: string }
  variants: { key: string; is_control: boolean; units: number; expected_share: number }[]
  srm: { chi2: number; p_value: number; flag: boolean; max_abs_deviation: number }
  metrics: MetricResult[]
  segments: { dimension: string; value: string; metrics: MetricResult[] }[]
  timeseries: { date: string; metric: string; variant: string; lift: number; ci_low: number; ci_high: number }[]
  diagnostics: { check: string; status: 'pass' | 'warn' | 'fail'; detail: string }[]
  decision: { recommendation: Recommendation; variant?: string; reason: string } | null
  sample?: boolean
}

export interface PowerRequest {
  baseline_mean: number
  variance: number
  n_per_day: number
  arms: number
  alpha: number
  power: number
  cuped_rho2: number
  days?: number
  target_mde?: number
}

export interface PowerResult {
  mde: number
  days: number
  days_to_mde: number | null
  n_per_arm: number
  curve?: { days: number; mde: number }[]
  source?: string
}

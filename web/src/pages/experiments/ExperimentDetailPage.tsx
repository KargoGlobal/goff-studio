import { useMemo, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import {
  AlertOctagon,
  AlertTriangle,
  ArrowLeft,
  CheckCircle2,
  Clipboard,
  Download,
  Pencil,
  RefreshCw,
  XCircle,
} from 'lucide-react'
import { useExperiment, useExperimentResults } from '@/hooks/useExperiments'
import { Badge, Button, Card, Code, Spinner } from '@/components/ui/primitives'
import { useToast } from '@/components/ui/Toast'
import { RecommendationBadge, SampleBadge, StatusBadge } from '@/components/experiments/badges'
import { ArmBars } from '@/components/experiments/ArmBars'
import { LiftChart } from '@/components/experiments/LiftChart'
import { MetricsTable } from '@/components/experiments/MetricsTable'
import {
  DECISION_LABEL,
  confidenceLevel,
  daysBetween,
  describeTiming,
  formatDate,
  formatPValue,
  groupByRole,
  liftSeries,
  readoutMarkdown,
} from '@/lib/experiments'
import type { ExperimentView, Results } from '@/lib/experimentTypes'
import { cn } from '@/lib/cn'

function Meta({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <dt className="text-[11px] font-medium uppercase tracking-wide text-ink-muted">{label}</dt>
      <dd className="mt-0.5 text-[13px] text-ink">{children}</dd>
    </div>
  )
}

function freshness(asOf: string): string {
  const t = Date.parse(asOf)
  if (Number.isNaN(t)) return asOf
  const hours = Math.max(0, Math.round((Date.now() - t) / 3_600_000))
  const ago = hours < 1 ? 'less than an hour ago' : hours < 48 ? `${hours}h ago` : `${Math.round(hours / 24)}d ago`
  return `${asOf.replace('T', ' ').replace('Z', ' UTC')} (${ago})`
}

function Header({ e, results }: { e: ExperimentView; results?: Results }) {
  const toast = useToast()
  const canEdit = e.actions.includes('edit_rules')

  async function copy() {
    if (!results) return
    const md = readoutMarkdown(e, results)
    try {
      await navigator.clipboard.writeText(md)
      toast('Readout copied as Markdown.')
    } catch {
      toast('Could not reach the clipboard; use Download instead.', 'error')
    }
  }

  function download() {
    if (!results) return
    const blob = new Blob([readoutMarkdown(e, results)], { type: 'text/markdown' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `${e.key}-readout.md`
    a.click()
    URL.revokeObjectURL(url)
  }

  return (
    <div className="flex flex-wrap items-start justify-between gap-4">
      <div className="min-w-0">
        <Link to="/experiments" className="mb-2 inline-flex items-center gap-1 text-[12.5px] text-ink-muted hover:text-ink">
          <ArrowLeft className="h-3.5 w-3.5" /> Experiments
        </Link>
        <h1 className="flex flex-wrap items-center gap-2 text-xl font-semibold tracking-tight">
          {e.name}
          <StatusBadge status={e.status} />
          {results?.sample && <SampleBadge />}
        </h1>
        <p className="font-mono text-[12px] text-ink-muted">{e.key}</p>
      </div>
      <div className="flex gap-2">
        <Button variant="outline" size="sm" onClick={() => void copy()} disabled={!results || results.status !== 'ok'}>
          <Clipboard className="h-3.5 w-3.5" /> Copy readout
        </Button>
        <Button variant="outline" size="sm" onClick={download} disabled={!results || results.status !== 'ok'} aria-label="Download readout as Markdown">
          <Download className="h-3.5 w-3.5" />
        </Button>
        {canEdit && (
          <Link to={`/experiments/${encodeURIComponent(e.key)}/edit`}>
            <Button size="sm">
              <Pencil className="h-3.5 w-3.5" /> Edit
            </Button>
          </Link>
        )}
      </div>
    </div>
  )
}

function Overview({ e, results }: { e: ExperimentView; results?: Results }) {
  const method = results?.method ?? { test: e.analysis.test, alpha: e.analysis.alpha, cuped: e.analysis.cuped, correction: e.analysis.correction }
  return (
    <Card className="p-5">
      <p className="text-[15px] leading-relaxed text-ink">{e.hypothesis}</p>
      <dl className="mt-4 grid grid-cols-2 gap-4 md:grid-cols-4">
        <Meta label="Flag">
          <Link to={`/env/${e.environment}/flags/${encodeURIComponent(e.flag)}`} className="font-mono text-brand hover:underline">
            {e.flag}
          </Link>{' '}
          <span className="text-ink-muted">in {e.environment}</span>
        </Meta>
        <Meta label="Window">
          {formatDate(e.start)} → {formatDate(e.end)}{' '}
          <span className="text-ink-muted">
            ({daysBetween(e.start, e.end)}d{e.extended ? ', extended' : ''})
          </span>
          <p className="text-[12px] text-ink-muted">{describeTiming(e)}</p>
        </Meta>
        <Meta label="Unit">
          {e.unit.type} <span className="text-ink-muted">by {e.unit.key}</span>
        </Meta>
        <Meta label="Method">
          {method.test}, alpha {method.alpha}
          {method.cuped ? `, CUPED${e.analysis.covariate ? ` (${e.analysis.covariate})` : ''}` : ''}
          {method.correction !== 'none' ? `, ${method.correction}` : ''}
        </Meta>
        <Meta label="Owner">{e.owner}</Meta>
        <Meta label="Arms">
          {e.variants.join(', ')} <span className="text-ink-muted">(control {e.control})</span>
        </Meta>
        <Meta label="Allocations">{e.allocations.join(', ')}</Meta>
        {e.ticket && <Meta label="Ticket">{e.ticket}</Meta>}
      </dl>
    </Card>
  )
}

function DecisionCard({ e, results }: { e: ExperimentView; results: Results }) {
  const d = results.decision
  return (
    <Card className="p-5">
      <div className="flex flex-wrap items-center gap-2">
        <h2 className="text-[15px] font-semibold">Recommendation</h2>
        <RecommendationBadge value={d?.recommendation} />
        {d?.variant && <Code>{d.variant}</Code>}
      </div>
      <p className="mt-2 text-[13.5px] text-ink-soft">{d?.reason ?? 'No recommendation yet.'}</p>
      {e.decision && (
        <p className="mt-3 border-t pt-3 text-[13px] text-ink">
          <strong>Recorded decision:</strong> {DECISION_LABEL[e.decision.outcome] ?? e.decision.outcome}
          {e.decision.variant && <> ({e.decision.variant})</>}
          {e.decision.by && <> by {e.decision.by}</>}
          {e.decision.at && <> on {formatDate(e.decision.at)}</>}
          {e.decision.note && <span className="text-ink-soft"> — {e.decision.note}</span>}
        </p>
      )}
    </Card>
  )
}

function SRMBanner({ results }: { results: Results }) {
  if (!results.srm.flag) {
    return (
      <p className="flex items-center gap-2 text-[13px] text-ok">
        <CheckCircle2 className="h-4 w-4" aria-hidden />
        Traffic matches the planned split (p = {formatPValue(results.srm.p_value)}).
      </p>
    )
  }
  return (
    <div role="alert" className="flex items-start gap-3 rounded-xl border border-danger bg-danger-soft p-4">
      <AlertOctagon className="mt-0.5 h-5 w-5 shrink-0 text-danger" aria-hidden />
      <div>
        <p className="font-semibold text-danger">Sample ratio mismatch: do not act on these results</p>
        <p className="mt-1 text-[13px] text-ink">
          The observed split differs from the plan (χ² = {results.srm.chi2.toFixed(1)}, p = {formatPValue(results.srm.p_value)}, max
          deviation {(results.srm.max_abs_deviation * 100).toFixed(2)} points). Assignment or logging is broken somewhere; find the
          cause before reading any metric below.
        </p>
      </div>
    </div>
  )
}

function Segments({ results, level, cuped }: { results: Results; level: string; cuped: boolean }) {
  const dims = useMemo(() => [...new Set(results.segments.map((s) => s.dimension))], [results])
  const [active, setActive] = useState(0)
  if (dims.length === 0) return null
  const dim = dims[Math.min(active, dims.length - 1)]

  return (
    <Card className="p-5">
      <h2 className="mb-3 text-[15px] font-semibold">Segments</h2>
      <div role="tablist" aria-label="Segment dimension" className="mb-4 flex flex-wrap gap-1 border-b">
        {dims.map((d, i) => (
          <button
            key={d}
            role="tab"
            type="button"
            aria-selected={d === dim}
            onClick={() => setActive(i)}
            className={cn(
              '-mb-px border-b-2 px-3 py-1.5 text-[13px]',
              d === dim ? 'border-brand font-medium text-brand' : 'border-transparent text-ink-muted hover:text-ink',
            )}
          >
            {d}
          </button>
        ))}
      </div>
      <div role="tabpanel" className="space-y-5">
        {results.segments
          .filter((s) => s.dimension === dim)
          .map((s) => (
            <div key={s.value}>
              <p className="mb-1.5 text-[13px] font-medium">
                {dim} = <Code>{s.value}</Code>
              </p>
              <MetricsTable role="primary" metrics={s.metrics} level={level} showCuped={cuped} />
            </div>
          ))}
      </div>
    </Card>
  )
}

function CumulativeLift({ e, results }: { e: ExperimentView; results: Results }) {
  const metrics = useMemo(() => {
    const withData = new Set(results.timeseries.map((p) => p.metric))
    return results.metrics.filter((m) => withData.has(m.key))
  }, [results])
  const [metric, setMetric] = useState(metrics[0]?.key ?? '')
  const series = useMemo(() => liftSeries(results, metric), [results, metric])
  if (metrics.length === 0) return null

  return (
    <Card className="p-5">
      <div className="mb-3 flex items-center justify-between gap-2">
        <h2 className="text-[15px] font-semibold">Cumulative lift</h2>
        {metrics.length > 1 && (
          <select
            value={metric}
            onChange={(ev) => setMetric(ev.target.value)}
            aria-label="Metric for the lift chart"
            className="h-8 rounded-md border bg-surface px-2 text-[13px]"
          >
            {metrics.map((m) => (
              <option key={m.key} value={m.key}>
                {m.name}
              </option>
            ))}
          </select>
        )}
      </div>
      <LiftChart
        series={series}
        variants={e.variants}
        control={e.control}
        metricName={metrics.find((m) => m.key === metric)?.name ?? metric}
      />
    </Card>
  )
}

function Diagnostics({ results }: { results: Results }) {
  if (results.diagnostics.length === 0) return null
  const icon = {
    pass: <CheckCircle2 className="h-4 w-4 text-ok" aria-label="pass" />,
    warn: <AlertTriangle className="h-4 w-4 text-warn" aria-label="warning" />,
    fail: <XCircle className="h-4 w-4 text-danger" aria-label="fail" />,
  }
  return (
    <Card className="p-5">
      <h2 className="mb-3 text-[15px] font-semibold">Diagnostics</h2>
      <ul className="space-y-2">
        {results.diagnostics.map((d) => (
          <li key={d.check} className="flex items-start gap-2 text-[13px]">
            <span className="mt-0.5 shrink-0">{icon[d.status] ?? icon.warn}</span>
            <span>
              <span className="font-medium">{d.check.replace(/_/g, ' ')}</span>
              <span className="text-ink-soft"> — {d.detail}</span>
            </span>
          </li>
        ))}
      </ul>
    </Card>
  )
}

function ResultsBody({ e, results }: { e: ExperimentView; results: Results }) {
  const groups = groupByRole(results.metrics)
  const level = confidenceLevel(results.method.alpha)
  const cuped = results.method.cuped

  return (
    <div className="space-y-5">
      <SRMBanner results={results} />
      <div className={cn('space-y-5', results.srm.flag && 'opacity-60')}>
        <DecisionCard e={e} results={results} />
        <Card className="space-y-6 p-5">
          <div className="flex flex-wrap items-baseline justify-between gap-2">
            <h2 className="text-[15px] font-semibold">Metrics</h2>
            <p className="text-[12px] text-ink-muted">
              Lift is relative to {e.control}; intervals are {level}
              {results.method.test === 'sequential' ? ' always-valid (sequential)' : ''}.
            </p>
          </div>
          {(['primary', 'guardrail', 'secondary'] as const).map((role) => (
            <MetricsTable key={role} role={role} metrics={groups[role]} level={level} showCuped={cuped} />
          ))}
        </Card>
        <div className="grid gap-5 lg:grid-cols-[2fr_1fr]">
          <CumulativeLift e={e} results={results} />
          <Card className="p-5">
            <h2 className="mb-3 text-[15px] font-semibold">Traffic</h2>
            <ArmBars arms={results.variants} variants={e.variants} control={e.control} />
            <p className="mt-3 text-[11.5px] text-ink-muted">Tick marks show the planned share.</p>
          </Card>
        </div>
        <Segments results={results} level={level} cuped={cuped} />
        <Diagnostics results={results} />
      </div>
    </div>
  )
}

export function ExperimentDetailPage() {
  const { key = '' } = useParams()
  const experiment = useExperiment(key)
  const results = useExperimentResults(key)

  if (experiment.isLoading) {
    return (
      <div className="flex items-center gap-2 text-ink-muted">
        <Spinner /> <span>Loading experiment…</span>
      </div>
    )
  }
  if (experiment.error || !experiment.data) {
    return (
      <Card className="border-danger bg-danger-soft p-4 text-[13px] text-ink">
        {experiment.error?.message ?? 'That experiment does not exist'}
      </Card>
    )
  }
  const e = experiment.data
  const r = results.data

  return (
    <div className="space-y-5">
      <Header e={e} results={r} />
      <Overview e={e} results={r} />

      {results.isLoading && (
        <div className="flex items-center gap-2 text-ink-muted">
          <Spinner /> <span>Fetching results…</span>
        </div>
      )}
      {results.error && (
        <Card className="flex items-start justify-between gap-4 border-warn bg-warn-soft p-4">
          <p className="flex items-start gap-2 text-[13px] text-ink">
            <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-warn" aria-hidden />
            {results.error.message}
          </p>
          <Button variant="outline" size="sm" onClick={() => void results.refetch()} disabled={results.isFetching}>
            <RefreshCw className={cn('h-3.5 w-3.5', results.isFetching && 'animate-spin')} /> Retry
          </Button>
        </Card>
      )}
      {r && r.status !== 'ok' && (
        <Card className="p-4 text-[13px] text-ink-soft">{r.message ?? 'Results are not available yet.'}</Card>
      )}
      {r && r.status === 'ok' && <ResultsBody e={e} results={r} />}
      {r && (
        <p className="text-[12px] text-ink-muted">
          <Badge tone="neutral" className="mr-2">
            data as of
          </Badge>
          {freshness(r.as_of)}
          {r.sample && ' · sample data generated by Studio because no analysis service is configured'}
        </p>
      )}
    </div>
  )
}

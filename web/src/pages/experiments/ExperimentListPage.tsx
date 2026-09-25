import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { AlertTriangle, FlaskConical, Plus, Search } from 'lucide-react'
import { useCachedResults, useExperiments, usePrefetchResults } from '@/hooks/useExperiments'
import { Badge, Button, Card, Code, Input, Spinner } from '@/components/ui/primitives'
import { RecommendationBadge, SampleBadge, SRMBadge, StatusBadge } from '@/components/experiments/badges'
import { describeTiming, formatLift } from '@/lib/experiments'
import type { ExperimentView, ResultSummary } from '@/lib/experimentTypes'
import { cn } from '@/lib/cn'

const STATUSES = ['all', 'running', 'draft', 'stopped', 'concluded'] as const

// Prefer the list's own summary; fall back to results a hover already prefetched.
function useSummary(e: ExperimentView): ResultSummary | undefined {
  const cached = useCachedResults(e.results ? undefined : e.key)
  if (e.results) return e.results
  const r = cached.data
  if (!r) return undefined
  const primary = r.metrics.find((m) => m.role === 'primary')
  const pick = primary?.results.find((a) => a.variant === r.decision?.variant) ?? primary?.results[0]
  return {
    status: r.status,
    asOf: r.as_of,
    srmFlag: r.srm.flag,
    primaryMetric: primary?.key,
    primaryVariant: pick?.variant,
    primaryLift: pick?.lift ?? undefined,
    primarySignificant: pick?.significant ?? false,
    recommendation: r.decision?.recommendation,
    sample: Boolean(r.sample),
  }
}

function Row({ e, onHover }: { e: ExperimentView; onHover: (key: string) => void }) {
  const summary = useSummary(e)
  const hasData = summary && summary.status === 'ok'
  return (
    <tr
      className="border-b last:border-0 hover:bg-canvas/60"
      onMouseEnter={() => onHover(e.key)}
      onFocus={() => onHover(e.key)}
    >
      <td className="px-4 py-3 align-top">
        <Link to={`/experiments/${encodeURIComponent(e.key)}`} className="font-medium text-brand hover:underline">
          {e.name}
        </Link>
        <p className="font-mono text-[11.5px] text-ink-muted">{e.key}</p>
      </td>
      <td className="px-4 py-3 align-top">
        <StatusBadge status={e.status} />
      </td>
      <td className="px-4 py-3 align-top">
        <Badge tone="neutral">{e.owner}</Badge>
      </td>
      <td className="px-4 py-3 align-top">
        <Code>{e.flag}</Code>
        <p className="mt-0.5 text-[11.5px] text-ink-muted">{e.environment}</p>
      </td>
      <td className="px-4 py-3 align-top text-[13px] text-ink-soft">{e.unit.type}</td>
      <td className="px-4 py-3 align-top text-[13px] tabular-nums text-ink-soft">{describeTiming(e)}</td>
      <td className="px-4 py-3 align-top">{hasData ? <SRMBadge flag={summary.srmFlag} /> : <span className="text-ink-muted">—</span>}</td>
      <td className="px-4 py-3 text-right align-top tabular-nums">
        {hasData && summary.primaryLift !== undefined ? (
          <span
            className={cn('text-[13px]', summary.primarySignificant ? 'font-semibold text-ink' : 'text-ink-soft')}
            title={`${summary.primaryMetric} for ${summary.primaryVariant}${summary.primarySignificant ? ', significant' : ', not significant'}`}
          >
            {formatLift(summary.primaryLift)}
          </span>
        ) : (
          <span className="text-ink-muted">—</span>
        )}
      </td>
      <td className="px-4 py-3 align-top">
        {e.decision ? (
          <Badge tone="brand" title="Recorded decision">
            {e.decision.outcome.replace(/_/g, ' ')}
          </Badge>
        ) : (
          <RecommendationBadge value={hasData ? summary.recommendation : undefined} />
        )}
      </td>
    </tr>
  )
}

export function ExperimentListPage() {
  const { data, isLoading, error } = useExperiments()
  const prefetch = usePrefetchResults()
  const [status, setStatus] = useState<(typeof STATUSES)[number]>('all')
  const [team, setTeam] = useState('all')
  const [search, setSearch] = useState('')

  const teams = useMemo(() => [...new Set((data?.experiments ?? []).map((e) => e.owner))].sort(), [data])
  const rows = useMemo(() => {
    const q = search.trim().toLowerCase()
    return (data?.experiments ?? []).filter((e) => {
      if (status !== 'all' && e.status !== status) return false
      if (team !== 'all' && e.owner !== team) return false
      if (!q) return true
      return e.key.includes(q) || e.name.toLowerCase().includes(q) || e.flag.toLowerCase().includes(q)
    })
  }, [data, status, team, search])

  if (isLoading) {
    return (
      <div className="flex items-center gap-2 text-ink-muted">
        <Spinner />
        <span>Loading experiments…</span>
      </div>
    )
  }
  if (error) {
    return <Card className="border-danger bg-danger-soft p-4 text-[13px] text-ink">{error.message}</Card>
  }

  return (
    <div className="space-y-5">
      <div className="flex items-baseline justify-between gap-4">
        <div>
          <h1 className="flex items-center gap-2 text-xl font-semibold tracking-tight">
            Experiments {data?.sample && <SampleBadge />}
          </h1>
          <p className="mt-0.5 text-[13px] text-ink-muted">
            {data?.experiments.length ?? 0} experiments in the registry
          </p>
        </div>
        <Link to="/experiments/new">
          <Button size="sm">
            <Plus className="h-3.5 w-3.5" />
            New experiment
          </Button>
        </Link>
      </div>

      {(data?.broken.length ?? 0) > 0 && (
        <Card className="border-warn bg-warn-soft p-4">
          <p className="flex items-center gap-2 text-[13px] font-medium text-warn">
            <AlertTriangle className="h-4 w-4" />
            Some registry files could not be read
          </p>
          <ul className="mt-2 space-y-1 text-[12.5px] text-ink">
            {data!.broken.map((b) => (
              <li key={b.file}>
                <Code>{b.file}</Code> — {b.reason}
              </li>
            ))}
          </ul>
        </Card>
      )}

      <div className="flex flex-wrap items-center gap-2">
        <div className="relative w-72">
          <Search className="pointer-events-none absolute left-3 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-ink-muted" />
          <Input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search experiments"
            className="pl-8"
            aria-label="Search experiments"
          />
        </div>
        <select
          value={status}
          onChange={(e) => setStatus(e.target.value as (typeof STATUSES)[number])}
          aria-label="Filter by status"
          className="h-9 rounded-md border bg-surface px-2.5 text-sm text-ink focus:border-brand focus:outline-none"
        >
          {STATUSES.map((s) => (
            <option key={s} value={s}>
              {s === 'all' ? 'All statuses' : s}
            </option>
          ))}
        </select>
        <select
          value={team}
          onChange={(e) => setTeam(e.target.value)}
          aria-label="Filter by team"
          className="h-9 rounded-md border bg-surface px-2.5 text-sm text-ink focus:border-brand focus:outline-none"
        >
          <option value="all">All teams</option>
          {teams.map((t) => (
            <option key={t} value={t}>
              {t}
            </option>
          ))}
        </select>
        <span className="ml-auto text-[13px] text-ink-muted">{rows.length} shown</span>
      </div>

      <Card className="overflow-x-auto">
        <table className="w-full min-w-[900px]">
          <thead className="bg-canvas">
            <tr className="border-b text-left text-[11px] font-medium uppercase tracking-wide text-ink-muted">
              <th className="px-4 py-2.5">Experiment</th>
              <th className="px-4 py-2.5">Status</th>
              <th className="px-4 py-2.5">Owner</th>
              <th className="px-4 py-2.5">Flag</th>
              <th className="px-4 py-2.5">Unit</th>
              <th className="px-4 py-2.5">Timing</th>
              <th className="px-4 py-2.5">SRM</th>
              <th className="px-4 py-2.5 text-right">Primary lift</th>
              <th className="px-4 py-2.5">Decision</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((e) => (
              <Row key={e.key} e={e} onHover={prefetch} />
            ))}
            {rows.length === 0 && (
              <tr>
                <td colSpan={9} className="px-4 py-12 text-center text-[13px] text-ink-muted">
                  <FlaskConical className="mx-auto mb-2 h-5 w-5" />
                  {data?.experiments.length ? 'No experiments match' : 'No experiments yet. Create one to get started.'}
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </Card>
    </div>
  )
}

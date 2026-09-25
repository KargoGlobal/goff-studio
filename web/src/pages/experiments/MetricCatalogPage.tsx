import { useState, type ReactNode } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { ArrowLeft, Plus } from 'lucide-react'
import { api, type DiffResult } from '@/lib/api'
import { useMetrics, useSaveMetric } from '@/hooks/useExperiments'
import { Badge, Button, Card, Code, Input, Spinner } from '@/components/ui/primitives'
import { ReviewDialog } from '@/components/ReviewDialog'
import { useToast } from '@/components/ui/Toast'
import type { Metric } from '@/lib/experimentTypes'

const select = 'h-9 w-full rounded-md border bg-surface px-2.5 text-sm text-ink focus:border-brand focus:outline-none'

const BLANK: Metric = {
  key: '',
  name: '',
  kind: 'mean',
  numerator: '',
  format: 'number',
  direction: 'increase',
  cap: null,
  description: '',
}

export function MetricCatalogPage() {
  const { data, isLoading, error } = useMetrics()
  const canCreate = data?.actions.includes('create')

  if (isLoading) {
    return (
      <div className="flex items-center gap-2 text-ink-muted">
        <Spinner /> <span>Loading metrics…</span>
      </div>
    )
  }
  if (error) return <Card className="border-danger bg-danger-soft p-4 text-[13px]">{error.message}</Card>

  return (
    <div className="space-y-5">
      <div className="flex items-baseline justify-between gap-4">
        <div>
          <h1 className="text-xl font-semibold tracking-tight">Metric catalog</h1>
          <p className="mt-0.5 text-[13px] text-ink-muted">
            {data?.metrics.length ?? 0} metrics experiments can use. Each is a column in the per-unit aggregate table.
          </p>
        </div>
        {canCreate && (
          <Link to="/metrics/new">
            <Button size="sm">
              <Plus className="h-3.5 w-3.5" /> New metric
            </Button>
          </Link>
        )}
      </div>
      <Card className="overflow-x-auto">
        <table className="w-full min-w-[760px] text-[13px]">
          <thead className="bg-canvas">
            <tr className="border-b text-left text-[11px] font-medium uppercase tracking-wide text-ink-muted">
              <th className="px-4 py-2.5">Metric</th>
              <th className="px-4 py-2.5">Kind</th>
              <th className="px-4 py-2.5">Definition</th>
              <th className="px-4 py-2.5">Format</th>
              <th className="px-4 py-2.5">Better when</th>
            </tr>
          </thead>
          <tbody>
            {(data?.metrics ?? []).map((m) => (
              <tr key={m.key} className="border-b last:border-0 hover:bg-canvas/60">
                <td className="px-4 py-3 align-top">
                  <Link to={`/metrics/${encodeURIComponent(m.key)}`} className="font-medium text-brand hover:underline">
                    {m.name}
                  </Link>
                  <p className="font-mono text-[11.5px] text-ink-muted">{m.key}</p>
                  {m.description && <p className="mt-0.5 text-[12px] text-ink-soft">{m.description}</p>}
                </td>
                <td className="px-4 py-3 align-top">
                  <Badge>{m.kind}</Badge>
                </td>
                <td className="px-4 py-3 align-top">
                  <Code>{m.denominator ? `${m.numerator} / ${m.denominator}` : `mean(${m.numerator})`}</Code>
                  {m.cap && <p className="mt-1 text-[11.5px] text-ink-muted">winsorized at p{m.cap.pct}</p>}
                </td>
                <td className="px-4 py-3 align-top text-ink-soft">{m.format}</td>
                <td className="px-4 py-3 align-top text-ink-soft">{m.direction === 'decrease' ? 'lower' : 'higher'}</td>
              </tr>
            ))}
            {(data?.metrics.length ?? 0) === 0 && (
              <tr>
                <td colSpan={5} className="px-4 py-12 text-center text-ink-muted">
                  The catalog is empty. Metrics live in <Code>metrics/&lt;key&gt;.yaml</Code>.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </Card>
    </div>
  )
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="block">
      <span className="text-[12.5px] font-medium text-ink">{label}</span>
      <div className="mt-1">{children}</div>
    </label>
  )
}

export function MetricEditPage() {
  const { key } = useParams()
  const { data, isLoading } = useMetrics()
  if (isLoading) return <Spinner />
  if (!key) return <MetricForm initial={BLANK} creating canSave={Boolean(data?.actions.includes('create'))} />
  const existing = data?.metrics.find((m) => m.key === key)
  if (!existing) {
    return <Card className="border-danger bg-danger-soft p-4 text-[13px]">That metric is not in the catalog.</Card>
  }
  const { fileSha, actions, ...metric } = existing
  return <MetricForm key={fileSha} initial={metric} fileSha={fileSha} creating={false} canSave={actions.includes('edit_rules')} />
}

function MetricForm({
  initial,
  creating,
  fileSha,
  canSave,
}: {
  initial: Metric
  creating: boolean
  fileSha?: string
  canSave: boolean
}) {
  const navigate = useNavigate()
  const toast = useToast()
  const save = useSaveMetric()
  const [draft, setDraft] = useState<Metric>(initial)
  const [problem, setProblem] = useState<string | null>(null)
  const [reviewing, setReviewing] = useState(false)
  const [diff, setDiff] = useState<DiffResult | undefined>()
  const [loadingDiff, setLoadingDiff] = useState(false)

  const set = (patch: Partial<Metric>) => setDraft((d) => ({ ...d, ...patch }))

  async function review() {
    setProblem(null)
    setReviewing(true)
    setLoadingDiff(true)
    try {
      setDiff(await api.diffMetric(draft, creating))
    } catch (e) {
      setReviewing(false)
      setProblem(e instanceof Error ? e.message : 'Could not prepare the change')
    } finally {
      setLoadingDiff(false)
    }
  }

  async function commit() {
    try {
      const result = await save.mutateAsync({ metric: draft, create: creating, fileSha })
      toast(result.message)
      navigate('/metrics')
    } catch (e) {
      toast(e instanceof Error ? e.message : 'Could not save', 'error')
      setReviewing(false)
    }
  }

  return (
    <div className="max-w-3xl space-y-5">
      <div>
        <Link to="/metrics" className="mb-2 inline-flex items-center gap-1 text-[12.5px] text-ink-muted hover:text-ink">
          <ArrowLeft className="h-3.5 w-3.5" /> Metric catalog
        </Link>
        <h1 className="text-xl font-semibold tracking-tight">{creating ? 'New metric' : draft.name}</h1>
      </div>
      <Card className="space-y-4 p-5">
        <div className="grid gap-4 md:grid-cols-2">
          <Field label="Key">
            <Input value={draft.key} disabled={!creating} onChange={(e) => set({ key: e.target.value.trim() })} aria-label="Key" className="font-mono" />
          </Field>
          <Field label="Name">
            <Input value={draft.name} onChange={(e) => set({ name: e.target.value })} aria-label="Name" />
          </Field>
        </div>
        <Field label="Description">
          <Input value={draft.description} onChange={(e) => set({ description: e.target.value })} aria-label="Description" />
        </Field>
        <div className="grid gap-4 md:grid-cols-3">
          <Field label="Kind">
            <select
              value={draft.kind}
              onChange={(e) => set({ kind: e.target.value as Metric['kind'], denominator: e.target.value === 'mean' ? undefined : draft.denominator })}
              aria-label="Kind"
              className={select}
            >
              <option value="mean">mean</option>
              <option value="ratio">ratio</option>
            </select>
          </Field>
          <Field label="Numerator column">
            <Input value={draft.numerator} onChange={(e) => set({ numerator: e.target.value.trim() })} aria-label="Numerator" className="font-mono" />
          </Field>
          {draft.kind === 'ratio' && (
            <Field label="Denominator column">
              <Input
                value={draft.denominator ?? ''}
                onChange={(e) => set({ denominator: e.target.value.trim() || undefined })}
                aria-label="Denominator"
                className="font-mono"
              />
            </Field>
          )}
        </div>
        <div className="grid gap-4 md:grid-cols-3">
          <Field label="Format">
            <select value={draft.format} onChange={(e) => set({ format: e.target.value as Metric['format'] })} aria-label="Format" className={select}>
              <option value="percent">percent</option>
              <option value="currency">currency</option>
              <option value="number">number</option>
            </select>
          </Field>
          <Field label="Better when it">
            <select
              value={draft.direction}
              onChange={(e) => set({ direction: e.target.value as Metric['direction'] })}
              aria-label="Direction"
              className={select}
            >
              <option value="increase">increases</option>
              <option value="decrease">decreases</option>
            </select>
          </Field>
          <Field label="Winsorize at percentile">
            <Input
              type="number"
              step="0.1"
              placeholder="off"
              value={draft.cap?.pct ?? ''}
              onChange={(e) => set({ cap: e.target.value === '' ? null : { pct: Number(e.target.value) } })}
              aria-label="Cap percentile"
              className="font-mono"
            />
          </Field>
        </div>
        {problem && (
          <p role="alert" className="rounded-md border border-danger bg-danger-soft p-3 text-[13px]">
            {problem}
          </p>
        )}
        <div className="flex justify-end gap-2">
          <Link to="/metrics">
            <Button variant="outline">Cancel</Button>
          </Link>
          <Button onClick={() => void review()} disabled={!canSave} title={canSave ? undefined : 'You cannot edit the shared catalog'}>
            {creating ? 'Review and add' : 'Review changes'}
          </Button>
        </div>
      </Card>
      <ReviewDialog
        open={reviewing}
        onClose={() => setReviewing(false)}
        onConfirm={() => void commit()}
        title="Review this change"
        diff={diff}
        loading={loadingDiff}
        saving={save.isPending}
        protectedEnv={false}
        envName=""
      />
    </div>
  )
}

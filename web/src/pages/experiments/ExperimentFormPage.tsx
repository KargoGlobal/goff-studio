import { useMemo, useState, type ReactNode } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { ArrowLeft, Plus, X } from 'lucide-react'
import { api, ApiError, type DiffResult, type Environment } from '@/lib/api'
import { useFlags, useMe } from '@/hooks/useFlags'
import { useExperiment, useMetrics, useSaveExperiment } from '@/hooks/useExperiments'
import { Button, Card, Input, Spinner } from '@/components/ui/primitives'
import { ChipInput } from '@/components/ChipInput'
import { ReviewDialog } from '@/components/ReviewDialog'
import { useToast } from '@/components/ui/Toast'
import { PowerPanel } from '@/components/experiments/PowerPanel'
import { blankExperiment, daysBetween, formProblems, MAX_WEEKS } from '@/lib/experiments'
import type { Experiment, ExperimentDecision, Metric } from '@/lib/experimentTypes'

const select = 'h-9 w-full rounded-md border bg-surface px-2.5 text-sm text-ink focus:border-brand focus:outline-none'

// A group of controls must not sit inside a <label>, or the first control inherits its name.
function Field({ label, hint, group, children }: { label: string; hint?: string; group?: boolean; children: ReactNode }) {
  const Tag = group ? 'div' : 'label'
  return (
    <Tag className="block">
      <span className="text-[12.5px] font-medium text-ink">{label}</span>
      {hint && <span className="ml-1.5 text-[11.5px] text-ink-muted">{hint}</span>}
      <div className="mt-1">{children}</div>
    </Tag>
  )
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <Card className="space-y-4 p-5">
      <h2 className="text-[15px] font-semibold">{title}</h2>
      {children}
    </Card>
  )
}

function CheckList({
  label,
  options,
  values,
  onChange,
}: {
  label: string
  options: { value: string; label?: string }[]
  values: string[]
  onChange: (next: string[]) => void
}) {
  if (options.length === 0) return <p className="text-[12.5px] text-ink-muted">Nothing to choose from yet.</p>
  return (
    <div role="group" aria-label={label} className="flex flex-wrap gap-x-4 gap-y-2">
      {options.map((o) => (
        <label key={o.value} className="flex items-center gap-1.5 text-[13px]">
          <input
            type="checkbox"
            checked={values.includes(o.value)}
            onChange={(e) =>
              onChange(e.target.checked ? [...values, o.value] : values.filter((v) => v !== o.value))
            }
            className="accent-[var(--color-brand)]"
          />
          <span className="font-mono">{o.label ?? o.value}</span>
        </label>
      ))}
    </div>
  )
}

function MetricPicker({
  label,
  catalog,
  values,
  taken,
  onChange,
}: {
  label: string
  catalog: Metric[]
  values: string[]
  taken: string[]
  onChange: (next: string[]) => void
}) {
  const options = catalog.filter((m) => values.includes(m.key) || !taken.includes(m.key))
  return (
    <CheckList
      label={label}
      options={options.map((m) => ({ value: m.key, label: m.name }))}
      values={values}
      onChange={onChange}
    />
  )
}

const dateOnly = (iso: string) => (iso ? iso.slice(0, 10) : '')
const fromDate = (d: string) => (d ? `${d}T00:00:00Z` : '')

export function ExperimentFormPage({ environments }: { environments: Environment[] }) {
  const { key } = useParams()
  const existing = useExperiment(key)

  if (!key) {
    return <ExperimentForm environments={environments} initial={{ ...blankExperiment(), environment: environments[0]?.name ?? '' }} creating />
  }
  if (existing.isLoading) {
    return (
      <div className="flex items-center gap-2 text-ink-muted">
        <Spinner /> <span>Loading experiment…</span>
      </div>
    )
  }
  if (existing.error || !existing.data) {
    return <Card className="border-danger bg-danger-soft p-4 text-[13px]">{existing.error?.message ?? 'That experiment does not exist'}</Card>
  }
  const { file: _f, flagFile: _ff, fileSha, actions: _a, daysRunning: _r, daysRemaining: _d, results: _res, ...rest } = existing.data
  const initial: Experiment = {
    ...rest,
    metrics: { primary: rest.metrics.primary ?? [], secondary: rest.metrics.secondary ?? [], guardrails: rest.metrics.guardrails ?? [] },
    analysis: { ...rest.analysis, strata: rest.analysis.strata ?? [] },
    segments: rest.segments ?? [],
  }
  return <ExperimentForm key={fileSha} environments={environments} initial={initial} fileSha={fileSha} creating={false} />
}

function ExperimentForm({
  environments,
  initial,
  creating,
  fileSha,
}: {
  environments: Environment[]
  initial: Experiment
  creating: boolean
  fileSha?: string
}) {
  const navigate = useNavigate()
  const toast = useToast()
  const { data: me } = useMe()
  const { data: catalogData } = useMetrics()
  const save = useSaveExperiment()

  const [draft, setDraft] = useState<Experiment>(initial)
  const [problems, setProblems] = useState<string[]>([])
  const [reviewing, setReviewing] = useState(false)
  const [diff, setDiff] = useState<DiffResult | undefined>()
  const [loadingDiff, setLoadingDiff] = useState(false)

  const env = draft.environment
  const environment = environments.find((e) => e.name === env)
  const { data: flagList } = useFlags(env || undefined)
  const flag = flagList?.flags.find((f) => f.key === draft.flag)
  const catalog = catalogData?.metrics ?? []

  const set = (patch: Partial<Experiment>) => setDraft((d) => ({ ...d, ...patch }))

  const taken = useMemo(() => {
    const g = (draft.metrics.guardrails ?? []).map((x) => x.metric)
    return {
      primary: [...(draft.metrics.secondary ?? []), ...g],
      secondary: [...draft.metrics.primary, ...g],
      guardrails: [...draft.metrics.primary, ...(draft.metrics.secondary ?? [])],
    }
  }, [draft])

  function pickFlag(name: string) {
    const f = flagList?.flags.find((x) => x.key === name)
    const variants = (f?.variations ?? []).map((v) => v.name)
    const rules = (f?.rules ?? []).map((r) => r.name)
    set({
      flag: name,
      owner: draft.owner || f?.team || '',
      allocations: rules.length === 1 ? rules : [],
      variants,
      control: variants.includes('control') ? 'control' : (variants[0] ?? ''),
    })
  }

  function setDecision(outcome: string) {
    if (!outcome) return set({ decision: null })
    const next: ExperimentDecision = {
      ...(draft.decision ?? {}),
      outcome: outcome as ExperimentDecision['outcome'],
    }
    if (!draft.decision) {
      next.by = me?.email
      next.at = new Date().toISOString().replace(/\.\d{3}Z$/, 'Z')
    }
    set({ decision: next })
  }

  async function review() {
    const local = formProblems(draft)
    setProblems(local)
    if (local.length > 0) return
    setReviewing(true)
    setLoadingDiff(true)
    setDiff(undefined)
    try {
      setDiff(await api.diffExperiment(draft, creating))
    } catch (e) {
      setReviewing(false)
      setProblems([e instanceof Error ? e.message : 'Could not prepare the change'])
    } finally {
      setLoadingDiff(false)
    }
  }

  async function commit() {
    try {
      const result = await save.mutateAsync({ experiment: draft, create: creating, fileSha })
      toast(result.message)
      navigate(`/experiments/${encodeURIComponent(draft.key)}`)
    } catch (e) {
      toast(e instanceof ApiError || e instanceof Error ? e.message : 'Could not save', 'error')
      setReviewing(false)
    }
  }

  const days = daysBetween(draft.start, draft.end)
  const guardrails = draft.metrics.guardrails ?? []

  return (
    <div className="space-y-5">
      <div>
        <Link
          to={creating ? '/experiments' : `/experiments/${encodeURIComponent(draft.key)}`}
          className="mb-2 inline-flex items-center gap-1 text-[12.5px] text-ink-muted hover:text-ink"
        >
          <ArrowLeft className="h-3.5 w-3.5" /> {creating ? 'Experiments' : draft.name}
        </Link>
        <h1 className="text-xl font-semibold tracking-tight">{creating ? 'New experiment' : `Edit ${draft.name}`}</h1>
      </div>

      <div className="grid gap-5 xl:grid-cols-[1fr_22rem]">
        <div className="space-y-5">
          <Section title="What and why">
            <div className="grid gap-4 md:grid-cols-2">
              <Field label="Key" hint="lowercase, digits, dots, dashes">
                <Input
                  value={draft.key}
                  disabled={!creating}
                  onChange={(e) => set({ key: e.target.value.trim() })}
                  className="font-mono"
                  aria-label="Key"
                />
              </Field>
              <Field label="Name">
                <Input value={draft.name} onChange={(e) => set({ name: e.target.value })} aria-label="Name" />
              </Field>
            </div>
            <Field label="Hypothesis" hint="what should change, and why">
              <textarea
                value={draft.hypothesis}
                onChange={(e) => set({ hypothesis: e.target.value })}
                aria-label="Hypothesis"
                rows={2}
                className="w-full rounded-md border bg-surface px-3 py-2 text-sm focus:border-brand focus:outline-none"
              />
            </Field>
            <div className="grid gap-4 md:grid-cols-3">
              <Field label="Owner" hint="team">
                <Input value={draft.owner} onChange={(e) => set({ owner: e.target.value.trim() })} aria-label="Owner" />
              </Field>
              <Field label="Ticket" hint="optional">
                <Input value={draft.ticket ?? ''} onChange={(e) => set({ ticket: e.target.value })} aria-label="Ticket" />
              </Field>
              <Field label="Status">
                <select
                  value={draft.status}
                  onChange={(e) => set({ status: e.target.value as Experiment['status'] })}
                  aria-label="Status"
                  className={select}
                >
                  {['draft', 'running', 'stopped', 'concluded'].map((s) => (
                    <option key={s}>{s}</option>
                  ))}
                </select>
              </Field>
            </div>
          </Section>

          <Section title="Flag and arms">
            <div className="grid gap-4 md:grid-cols-2">
              <Field label="Environment">
                <select
                  value={env}
                  disabled={!creating}
                  onChange={(e) => set({ environment: e.target.value, flag: '', allocations: [], variants: [], control: '' })}
                  aria-label="Environment"
                  className={select}
                >
                  {environments.map((e) => (
                    <option key={e.name} value={e.name}>
                      {e.display}
                    </option>
                  ))}
                </select>
              </Field>
              <Field label="Flag">
                <select value={draft.flag} onChange={(e) => pickFlag(e.target.value)} aria-label="Flag" className={select}>
                  <option value="">Choose a flag…</option>
                  {(flagList?.flags ?? []).map((f) => (
                    <option key={f.key} value={f.key}>
                      {f.key}
                    </option>
                  ))}
                </select>
              </Field>
            </div>
            {draft.flag && (
              <>
                <Field group label="Allocations" hint="the rules on the flag this experiment runs in">
                  <CheckList
                    label="Allocations"
                    options={(flag?.rules ?? []).map((r) => ({ value: r.name }))}
                    values={draft.allocations}
                    onChange={(allocations) => set({ allocations })}
                  />
                </Field>
                <div className="grid gap-4 md:grid-cols-2">
                  <Field group label="Variants">
                    <CheckList
                      label="Variants"
                      options={(flag?.variations ?? []).map((v) => ({ value: v.name }))}
                      values={draft.variants}
                      onChange={(variants) => set({ variants })}
                    />
                  </Field>
                  <Field label="Control">
                    <select
                      value={draft.control}
                      onChange={(e) => set({ control: e.target.value })}
                      aria-label="Control"
                      className={select}
                    >
                      {draft.variants.map((v) => (
                        <option key={v}>{v}</option>
                      ))}
                    </select>
                  </Field>
                </div>
              </>
            )}
            <div className="grid gap-4 md:grid-cols-2">
              <Field label="Randomization unit">
                <select
                  value={draft.unit.type}
                  onChange={(e) => set({ unit: { ...draft.unit, type: e.target.value as 'request' | 'entity' } })}
                  aria-label="Unit type"
                  className={select}
                >
                  <option value="request">request</option>
                  <option value="entity">entity</option>
                </select>
              </Field>
              <Field label="Unit key" hint="attribute hashed for assignment">
                <Input
                  value={draft.unit.key}
                  onChange={(e) => set({ unit: { ...draft.unit, key: e.target.value.trim() } })}
                  aria-label="Unit key"
                  className="font-mono"
                />
              </Field>
            </div>
            {draft.unit.type === 'entity' && (
              <p className="text-[12px] text-ink-muted">
                Entity units are analysed per entity, so repeated requests from one entity are not treated as independent.
              </p>
            )}
          </Section>

          <Section title="Window">
            <div className="flex flex-wrap items-end gap-4">
              <Field label="Start">
                <Input type="date" value={dateOnly(draft.start)} onChange={(e) => set({ start: fromDate(e.target.value) })} aria-label="Start date" className="w-44" />
              </Field>
              <Field label="End" hint="required">
                <Input type="date" value={dateOnly(draft.end)} onChange={(e) => set({ end: fromDate(e.target.value) })} aria-label="End date" className="w-44" />
              </Field>
              <label className="flex h-9 items-center gap-1.5 text-[13px]">
                <input
                  type="checkbox"
                  checked={Boolean(draft.extended)}
                  onChange={(e) => set({ extended: e.target.checked || undefined })}
                  className="accent-[var(--color-brand)]"
                />
                Extended beyond {MAX_WEEKS} weeks
              </label>
            </div>
            <p className={days > MAX_WEEKS * 7 && !draft.extended ? 'text-[12.5px] text-danger' : 'text-[12.5px] text-ink-muted'}>
              {Number.isFinite(days) ? `${days} days` : 'Pick both dates'}
              {days > MAX_WEEKS * 7 && !draft.extended && ` — longer than ${MAX_WEEKS} weeks; shorten it or mark it extended.`}
            </p>
          </Section>

          <Section title="Metrics">
            <Field group label="Primary" hint="the decision metric">
              <MetricPicker
                label="Primary metrics"
                catalog={catalog}
                values={draft.metrics.primary}
                taken={taken.primary}
                onChange={(primary) => set({ metrics: { ...draft.metrics, primary } })}
              />
            </Field>
            <Field group label="Secondary">
              <MetricPicker
                label="Secondary metrics"
                catalog={catalog}
                values={draft.metrics.secondary ?? []}
                taken={taken.secondary}
                onChange={(secondary) => set({ metrics: { ...draft.metrics, secondary } })}
              />
            </Field>
            <div>
              <p className="text-[12.5px] font-medium">
                Guardrails <span className="ml-1 font-normal text-ink-muted">metrics that must not drop by more than a tolerance</span>
              </p>
              <ul className="mt-2 space-y-2">
                {guardrails.map((g, i) => (
                  <li key={i} className="flex items-center gap-2">
                    <select
                      value={g.metric}
                      aria-label={`Guardrail ${i + 1} metric`}
                      onChange={(e) =>
                        set({ metrics: { ...draft.metrics, guardrails: guardrails.map((x, j) => (j === i ? { ...x, metric: e.target.value } : x)) } })
                      }
                      className={`${select} max-w-xs`}
                    >
                      <option value="">Choose…</option>
                      {catalog
                        .filter((m) => m.key === g.metric || !taken.guardrails.includes(m.key))
                        .map((m) => (
                          <option key={m.key} value={m.key}>
                            {m.name}
                          </option>
                        ))}
                    </select>
                    <span className="text-[12.5px] text-ink-muted">max drop</span>
                    <Input
                      type="number"
                      step="0.5"
                      value={g.max_drop_pct}
                      aria-label={`Guardrail ${i + 1} max drop`}
                      onChange={(e) =>
                        set({
                          metrics: {
                            ...draft.metrics,
                            guardrails: guardrails.map((x, j) => (j === i ? { ...x, max_drop_pct: Number(e.target.value) } : x)),
                          },
                        })
                      }
                      className="w-20 font-mono"
                    />
                    <span className="text-[12.5px] text-ink-muted">%</span>
                    <Button
                      variant="ghost"
                      size="sm"
                      aria-label={`Remove guardrail ${i + 1}`}
                      onClick={() => set({ metrics: { ...draft.metrics, guardrails: guardrails.filter((_, j) => j !== i) } })}
                    >
                      <X className="h-3.5 w-3.5" />
                    </Button>
                  </li>
                ))}
              </ul>
              <Button
                variant="outline"
                size="sm"
                className="mt-2"
                onClick={() => set({ metrics: { ...draft.metrics, guardrails: [...guardrails, { metric: '', max_drop_pct: 2 }] } })}
              >
                <Plus className="h-3.5 w-3.5" /> Add guardrail
              </Button>
            </div>
          </Section>

          <Section title="Analysis">
            <div className="grid gap-4 md:grid-cols-4">
              <Field label="Test">
                <select
                  value={draft.analysis.test}
                  onChange={(e) => set({ analysis: { ...draft.analysis, test: e.target.value as 'sequential' | 'fixed' } })}
                  aria-label="Test"
                  className={select}
                >
                  <option value="sequential">sequential</option>
                  <option value="fixed">fixed horizon</option>
                </select>
              </Field>
              <Field label="Alpha">
                <Input
                  type="number"
                  step="0.01"
                  value={draft.analysis.alpha}
                  onChange={(e) => set({ analysis: { ...draft.analysis, alpha: Number(e.target.value) } })}
                  aria-label="Alpha"
                  className="font-mono"
                />
              </Field>
              <Field label="Power">
                <Input
                  type="number"
                  step="0.05"
                  value={draft.analysis.power}
                  onChange={(e) => set({ analysis: { ...draft.analysis, power: Number(e.target.value) } })}
                  aria-label="Power"
                  className="font-mono"
                />
              </Field>
              <Field label="Correction">
                <select
                  value={draft.analysis.correction}
                  onChange={(e) => set({ analysis: { ...draft.analysis, correction: e.target.value as 'none' | 'holm' | 'bh' } })}
                  aria-label="Correction"
                  className={select}
                >
                  <option value="none">none</option>
                  <option value="holm">Holm</option>
                  <option value="bh">Benjamini-Hochberg</option>
                </select>
              </Field>
            </div>
            <div className="flex flex-wrap items-end gap-4">
              <label className="flex h-9 items-center gap-1.5 text-[13px]">
                <input
                  type="checkbox"
                  checked={draft.analysis.cuped}
                  onChange={(e) => set({ analysis: { ...draft.analysis, cuped: e.target.checked } })}
                  className="accent-[var(--color-brand)]"
                />
                CUPED variance reduction
              </label>
              {draft.analysis.cuped && (
                <Field label="Covariate" hint="pre-period column set">
                  <Input
                    value={draft.analysis.covariate ?? ''}
                    onChange={(e) => set({ analysis: { ...draft.analysis, covariate: e.target.value.trim() } })}
                    aria-label="Covariate"
                    className="w-64 font-mono"
                  />
                </Field>
              )}
            </div>
            <div className="grid gap-4 md:grid-cols-2">
              <Field group label="Strata" hint="post-stratification attributes">
                <ChipInput
                  label="Strata"
                  values={draft.analysis.strata ?? []}
                  onChange={(strata) => set({ analysis: { ...draft.analysis, strata } })}
                  placeholder="auction_type"
                />
              </Field>
              <Field group label="Segments" hint="breakdowns in the results">
                <ChipInput
                  label="Segments"
                  values={draft.segments ?? []}
                  onChange={(segments) => set({ segments })}
                  placeholder="media_type"
                />
              </Field>
            </div>
          </Section>

          {!creating && (
            <Section title="Decision">
              <div className="grid gap-4 md:grid-cols-3">
                <Field label="Outcome">
                  <select value={draft.decision?.outcome ?? ''} onChange={(e) => setDecision(e.target.value)} aria-label="Decision" className={select}>
                    <option value="">No decision yet</option>
                    <option value="roll_out">Roll out</option>
                    <option value="do_not_roll_out">Do not roll out</option>
                    <option value="extend">Extend</option>
                  </select>
                </Field>
                {draft.decision && (
                  <>
                    <Field label="Variant">
                      <select
                        value={draft.decision.variant ?? ''}
                        onChange={(e) => set({ decision: { ...draft.decision!, variant: e.target.value || undefined } })}
                        aria-label="Decision variant"
                        className={select}
                      >
                        <option value="">—</option>
                        {draft.variants.map((v) => (
                          <option key={v}>{v}</option>
                        ))}
                      </select>
                    </Field>
                    <Field label="Note">
                      <Input
                        value={draft.decision.note ?? ''}
                        onChange={(e) => set({ decision: { ...draft.decision!, note: e.target.value || undefined } })}
                        aria-label="Decision note"
                      />
                    </Field>
                  </>
                )}
              </div>
            </Section>
          )}

          {problems.length > 0 && (
            <Card role="alert" className="border-danger bg-danger-soft p-4 text-[13px] text-ink">
              <ul className="list-disc space-y-1 pl-5">
                {problems.map((p) => (
                  <li key={p}>{p}</li>
                ))}
              </ul>
            </Card>
          )}

          <div className="flex justify-end gap-2">
            <Link to={creating ? '/experiments' : `/experiments/${encodeURIComponent(draft.key)}`}>
              <Button variant="outline">Cancel</Button>
            </Link>
            <Button onClick={() => void review()}>{creating ? 'Review and create' : 'Review changes'}</Button>
          </div>
        </div>

        <div className="xl:sticky xl:top-0 xl:self-start">
          <PowerPanel experiment={draft} catalog={catalog} />
        </div>
      </div>

      <ReviewDialog
        open={reviewing}
        onClose={() => setReviewing(false)}
        onConfirm={() => void commit()}
        title="Review this change"
        diff={diff}
        loading={loadingDiff}
        saving={save.isPending}
        protectedEnv={Boolean(environment?.protected)}
        envName={environment?.name ?? env}
      />
    </div>
  )
}

import { useMemo, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import {
  ArrowDown,
  ArrowLeft,
  ArrowUp,
  GitCompare,
  History,
  Lock,
  Pencil,
  Play,
  Plus,
  Trash2,
  X,
} from 'lucide-react'
import {
  api,
  ApiError,
  type DiffResult,
  type Environment,
  type EvalResult,
  type Flag,
  type NewVariation,
  type Experimentation,
  type Outcome,
  type ProgressiveRollout,
} from '@/lib/api'
import { useMe } from '@/hooks/useFlags'
import {
  useAttributes,
  useAddRule,
  useSetRollout,
  useDeleteFlag,
  useRenameFlag,
  useSetProgressive,
  useSetExperimentation,
  useDeleteRule,
  useEditRule,
  useFlag,
  useHistory,
  useReorderRules,
  useSetState,
  useSetVariations,
} from '@/hooks/useFlags'
import { Badge, Button, Card, Code, Input, Spinner, Toggle } from '@/components/ui/primitives'
import { Select } from '@/components/ui/Select'
import { ReviewDialog } from '@/components/ReviewDialog'
import { useToast } from '@/components/ui/Toast'
import { describeOutcome } from '@/lib/describe'
import { RuleBuilder } from '@/components/RuleBuilder'
import { VariationsEditor } from '@/components/VariationsEditor'
import { ProgressiveEditor } from '@/components/ProgressiveEditor'
import { ExperimentationEditor } from '@/components/ExperimentationEditor'
import { groupFromCondition, queryFromGroup } from '@/lib/query'
import { tokensFromCondition } from '@/lib/tokens'
import { ConditionView } from '@/components/ConditionView'
import { ScheduleBadge } from '@/components/ScheduleBadge'
import { describeEffectiveState, effectiveState } from '@/lib/schedule'
import type { RuleGroupType } from 'react-querybuilder'

type Pending =
  | { kind: 'state'; enabled: boolean }
  | { kind: 'rollout'; ruleName: string; percentage: Record<string, number> }
  | { kind: 'rule'; ruleName: string; query: string }
  | { kind: 'outcome'; ruleName: string; outcome: Outcome }
  | { kind: 'ruleState'; ruleName: string; disabled: boolean }
  | { kind: 'addRule'; name: string; query: string; outcome: Outcome }
  | { kind: 'deleteRule'; ruleName: string }
  | { kind: 'order'; order: string[] }
  | { kind: 'variations'; variations: NewVariation[]; defaultVariation: string }
  | { kind: 'progressive'; ruleName: string; rollout: ProgressiveRollout }
  | { kind: 'progressiveClear'; ruleName: string; variation: string }
  | { kind: 'experimentation'; window: Experimentation }
  | { kind: 'experimentationClear' }
  | { kind: 'rename'; newKey: string }
  | { kind: 'deleteFlag' }

const emptyGroup: RuleGroupType = {
  combinator: 'and',
  rules: [{ field: '', operator: 'in', value: '' }],
}

export function FlagDetailPage({ environments }: { environments: Environment[] }) {
  const { env = '', key = '' } = useParams()
  const navigate = useNavigate()
  const environment = environments.find((e) => e.name === env)
  const { data: me } = useMe()
  const { data: flag, isLoading } = useFlag(env, key)
  const { data: commits } = useHistory(env, key, me?.capabilities?.history !== false)
  const { data: attributes } = useAttributes(env)
  const toast = useToast()

  const setState = useSetState(env)
  const editRule = useEditRule(env)
  const addRule = useAddRule(env)
  const deleteRule = useDeleteRule(env)
  const reorderRules = useReorderRules(env)
  const setVariations = useSetVariations(env)
  const setRollout = useSetRollout(env)
  const deleteFlag = useDeleteFlag(env)
  const renameFlag = useRenameFlag(env)
  const setProgressive = useSetProgressive(env)
  const setExperimentation = useSetExperimentation(env)

  const [pending, setPending] = useState<Pending | null>(null)
  const [diff, setDiff] = useState<DiffResult | undefined>()
  const [loadingDiff, setLoadingDiff] = useState(false)

  const [editingRule, setEditingRule] = useState<string | null>(null)
  const [draftGroup, setDraftGroup] = useState<RuleGroupType>(emptyGroup)
  const [editingVariations, setEditingVariations] = useState(false)
  const [addingRule, setAddingRule] = useState(false)
  const [renaming, setRenaming] = useState<string | null>(null)
  const [newRuleName, setNewRuleName] = useState('')
  const [newRuleVariation, setNewRuleVariation] = useState('')
  const [draftPct, setDraftPct] = useState<Record<string, Record<string, number>>>({})

  const [targetingKey, setTargetingKey] = useState('user-123')
  const [attrsText, setAttrsText] = useState('{\n  "tier": "gold"\n}')
  const [preview, setPreview] = useState<EvalResult | null>(null)
  const [previewing, setPreviewing] = useState(false)

  const variationNames = useMemo(() => (flag?.variations ?? []).map((v) => v.name), [flag])
  const rules = flag?.rules ?? []

  if (isLoading || !flag) {
    return (
      <div className="flex items-center gap-2 text-ink-muted">
        <Spinner />
        <span>Loading…</span>
      </div>
    )
  }

  const can = (action: string) => flag.actions.includes(action as never)
  const state = effectiveState(flag)
  const saving =
    setState.isPending ||
    editRule.isPending ||
    addRule.isPending ||
    deleteRule.isPending ||
    reorderRules.isPending ||
    setVariations.isPending ||
    setRollout.isPending ||
    deleteFlag.isPending ||
    renameFlag.isPending ||
    setProgressive.isPending ||
    setExperimentation.isPending

  async function openReview(next: Pending) {
    setPending(next)
    setLoadingDiff(true)
    setDiff(undefined)
    try {
      setDiff(await diffFor(next))
    } catch (e) {
      toast(e instanceof Error ? e.message : 'Could not prepare the change', 'error')
      setPending(null)
    } finally {
      setLoadingDiff(false)
    }
  }

  function diffFor(next: Pending) {
    switch (next.kind) {
      case 'state':
        return api.diffState(env, key, next.enabled)
      case 'rollout':
        return api.diffRollout(env, key, next.ruleName, next.percentage)
      case 'rule':
        return api.diffRule(env, key, next.ruleName, next.query)
      case 'outcome':
        return api.diffGeneric(env, key, {
          change: 'rule',
          ruleName: next.ruleName,
          outcome: next.outcome,
        })
      case 'ruleState':
        return api.diffGeneric(env, key, {
          change: 'rule',
          ruleName: next.ruleName,
          disabled: next.disabled,
        })
      case 'addRule':
        return api.diffGeneric(env, key, {
          change: 'addRule',
          name: next.name,
          query: next.query,
          outcome: next.outcome,
        })
      case 'deleteRule':
        return api.diffGeneric(env, key, { change: 'deleteRule', ruleName: next.ruleName })
      case 'order':
        return api.diffGeneric(env, key, { change: 'order', order: next.order })
      case 'variations':
        return api.diffGeneric(env, key, {
          change: 'variations',
          variations: next.variations,
          default: next.defaultVariation,
        })
      case 'progressive':
        return api.diffGeneric(env, key, {
          change: 'progressive',
          ruleName: next.ruleName,
          progressive: next.rollout,
        })
      case 'progressiveClear':
        return api.diffGeneric(env, key, {
          change: 'progressiveClear',
          ruleName: next.ruleName,
          variation: next.variation,
        })
      case 'experimentation':
        return api.diffGeneric(env, key, {
          change: 'experimentation',
          experimentation: next.window,
        })
      case 'experimentationClear':
        return api.diffGeneric(env, key, { change: 'experimentationClear' })
      case 'rename':
        return api.diffGeneric(env, key, { change: 'rename', name: next.newKey })
      case 'deleteFlag':
        return api.diffGeneric(env, key, { change: 'delete' })
    }
  }

  async function confirm() {
    if (!pending || !flag) return
    try {
      const result = await applyChange(flag, pending)
      toast(result.message)
      setDraftPct({})
      setEditingRule(null)
      setEditingVariations(false)
      setAddingRule(false)
      setRenaming(null)
      if (pending.kind === 'deleteFlag') navigate(`/env/${env}`)
      if (pending.kind === 'rename') {
        navigate(`/env/${env}/flags/${encodeURIComponent(pending.newKey)}`, { replace: true })
      }
    } catch (e) {
      if (e instanceof ApiError && e.isConflict) {
        toast(e.message, 'error')
      } else {
        toast(e instanceof Error ? e.message : 'Could not save', 'error')
      }
    } finally {
      setPending(null)
    }
  }

  function applyChange(target: Flag, change: Pending) {
    switch (change.kind) {
      case 'state':
        return setState.mutateAsync({ flag: target, enabled: change.enabled })
      case 'rollout':
        return setRollout.mutateAsync({
          flag: target,
          ruleName: change.ruleName,
          percentage: change.percentage,
        })
      case 'rule':
        return editRule.mutateAsync({
          flag: target,
          ruleName: change.ruleName,
          query: change.query,
        })
      case 'outcome':
        return editRule.mutateAsync({
          flag: target,
          ruleName: change.ruleName,
          outcome: change.outcome,
        })
      case 'ruleState':
        return editRule.mutateAsync({
          flag: target,
          ruleName: change.ruleName,
          disabled: change.disabled,
        })
      case 'addRule':
        return addRule.mutateAsync({
          flag: target,
          name: change.name,
          query: change.query,
          outcome: change.outcome,
        })
      case 'deleteRule':
        return deleteRule.mutateAsync({ flag: target, ruleName: change.ruleName })
      case 'order':
        return reorderRules.mutateAsync({ flag: target, order: change.order })
      case 'variations':
        return setVariations.mutateAsync({
          flag: target,
          variations: change.variations,
          defaultVariation: change.defaultVariation,
        })
      case 'progressive':
        return setProgressive.mutateAsync({
          flag: target,
          ruleName: change.ruleName,
          initial: change.rollout.initial,
          end: change.rollout.end,
        })
      case 'progressiveClear':
        return setProgressive.mutateAsync({
          flag: target,
          ruleName: change.ruleName,
          clear: true,
          variation: change.variation,
        })
      case 'experimentation':
        return setExperimentation.mutateAsync({
          flag: target,
          start: change.window.start,
          end: change.window.end,
        })
      case 'experimentationClear':
        return setExperimentation.mutateAsync({ flag: target, clear: true })
      case 'rename':
        return renameFlag.mutateAsync({ flag: target, newKey: change.newKey })
      case 'deleteFlag':
        return deleteFlag.mutateAsync(target)
    }
  }

  async function runPreview() {
    setPreviewing(true)
    try {
      let attributes: Record<string, unknown> = {}
      if (attrsText.trim()) attributes = JSON.parse(attrsText)
      setPreview(await api.preview(env, key, targetingKey, attributes))
    } catch (e) {
      toast(e instanceof SyntaxError ? 'Context must be valid JSON' : 'Preview failed', 'error')
    } finally {
      setPreviewing(false)
    }
  }

  function pctFor(ruleName: string, outcome: Outcome): Record<string, number> {
    if (draftPct[ruleName]) return draftPct[ruleName]
    if (outcome?.percentage) return outcome.percentage
    return Object.fromEntries(variationNames.map((n, i) => [n, i === 0 ? 100 : 0]))
  }

  function move(index: number, delta: number) {
    const order = rules.map((r) => r.name)
    const to = index + delta
    if (to < 0 || to >= order.length) return
    ;[order[index], order[to]] = [order[to], order[index]]
    void openReview({ kind: 'order', order })
  }

  return (
    <div className="space-y-6">
      <Link
        to={`/env/${env}`}
        className="inline-flex items-center gap-2 text-lg font-semibold text-ink-muted transition-colors hover:text-ink"
      >
        <ArrowLeft className="h-6 w-6" />
        All flags
      </Link>

      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0">
          {renaming === null ? (
            <div className="flex items-baseline gap-3">
              <h1 className="font-mono text-3xl font-bold tracking-tight">{flag.key}</h1>
              {can('create') && can('delete') && (
                <button
                  type="button"
                  onClick={() => setRenaming(flag.key)}
                  aria-label="Rename this flag"
                  title="Rename"
                  className="inline-flex h-8 w-8 items-center justify-center rounded-md text-brand transition-colors hover:text-brand-strong"
                >
                  <Pencil className="h-4 w-4" />
                </button>
              )}
            </div>
          ) : (
            <div className="flex items-center gap-2">
              <Input
                autoFocus
                value={renaming}
                onChange={(e) => setRenaming(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Escape') setRenaming(null)
                  if (e.key === 'Enter' && renaming.trim() && renaming.trim() !== flag.key) {
                    void openReview({ kind: 'rename', newKey: renaming.trim() })
                  }
                }}
                aria-label="New flag key"
                className="w-64 font-mono"
              />
              <Button
                size="sm"
                disabled={!renaming.trim() || renaming.trim() === flag.key}
                onClick={() => void openReview({ kind: 'rename', newKey: renaming.trim() })}
              >
                Review
              </Button>
              <Button size="sm" variant="outline" onClick={() => setRenaming(null)}>
                Cancel
              </Button>
            </div>
          )}
          <p className="mt-1.5 text-base text-ink-soft">{flag.summary}</p>
        </div>
        <div className="flex items-center gap-3">
          {environments.length > 1 && (
            <Link
              to={`/env/${env}/flags/${encodeURIComponent(key)}/compare`}
              className="inline-flex items-center gap-1.5 text-sm font-medium text-ink-muted transition-colors hover:text-ink"
            >
              <GitCompare className="h-4 w-4" />
              Compare
            </Link>
          )}
          {can('delete') && (
            <Button
              variant="ghost"
              onClick={() => void openReview({ kind: 'deleteFlag' })}
              aria-label={`Delete ${flag.key}`}
            >
              <Trash2 className="h-4 w-4" />
              Delete
            </Button>
          )}
          <ScheduleBadge flag={flag} />
          {!can('toggle') && <Lock className="h-3.5 w-3.5 text-ink-muted" />}
          <Toggle
            checked={flag.enabled}
            disabled={!can('toggle')}
            label={`Turn ${flag.key} ${flag.enabled ? 'off' : 'on'}`}
            onChange={(next) => void openReview({ kind: 'state', enabled: next })}
          />
        </div>
      </div>

      {state !== 'on' && state !== 'off' && (
        <div
          role="status"
          className="rounded-lg border border-warn bg-warn-soft px-3 py-2 text-[13px] text-warn"
        >
          This flag is turned on, but its schedule is closed, so every user gets the default value.{' '}
          {describeEffectiveState(state, flag.experimentation)}.
        </div>
      )}

      <div className="grid gap-4 lg:grid-cols-3">
        <div className="space-y-4 lg:col-span-2">
          <Card className="p-6">
            <div className="mb-3 flex items-center justify-between">
              <h2 className="text-xl font-semibold tracking-tight text-ink">
                Variations
              </h2>
              {can('edit_variations') && !editingVariations && (
                <Button
                  size="sm"
                  variant="ghost"
                  aria-label="Edit variations"
                  onClick={() => setEditingVariations(true)}
                >
                  <Pencil className="h-3.5 w-3.5" />
                  Edit
                </Button>
              )}
            </div>

            {editingVariations ? (
              <VariationsEditor
                flag={flag}
                onCancel={() => setEditingVariations(false)}
                onSubmit={(variations, defaultVariation) =>
                  void openReview({ kind: 'variations', variations, defaultVariation })
                }
              />
            ) : (
              <>
                <div className="space-y-1.5">
                  {(flag.variations ?? []).map((v) => (
                    <div
                      key={v.name}
                      className="flex items-center justify-between rounded-md border border-[color:var(--color-brand)] bg-surface px-3 py-2"
                    >
                      <span className="flex items-center gap-2">
                        <Code className="bg-transparent px-0 text-ink">{v.name}</Code>
                        {flag.default?.variation === v.name && <Badge tone="brand">default</Badge>}
                      </span>
                      <Code>{JSON.stringify(v.value)}</Code>
                    </div>
                  ))}
                </div>
                <p className="mt-3 text-[13px] text-ink-muted">
                  Type: {flag.type}
                  {(flag.preserved?.length ?? 0) > 0 && (
                    <> · Managed in the file: {flag.preserved?.join(', ')}</>
                  )}
                </p>

                <div className="mt-4 border-t pt-4">
                  <h3 className="mb-1 text-sm font-semibold text-ink-soft">
                    Schedule
                  </h3>
                  <p className="mb-2.5 text-[13px] text-ink-muted">
                    Turns the flag off outside a time window. Outside it the flag is off and
                    everyone gets the default value, exactly as if you had toggled it off.
                  </p>
                  <ExperimentationEditor
                    window={flag.experimentation}
                    disabled={!can('rollout')}
                    onSave={(next) => void openReview({ kind: 'experimentation', window: next })}
                    onRemove={() => void openReview({ kind: 'experimentationClear' })}
                  />
                </div>
              </>
            )}
          </Card>

          <Card className="p-6">
            <div className="mb-1 flex items-center justify-between">
              <h2 className="text-xl font-semibold tracking-tight text-ink">
                Targeting
              </h2>
              {can('edit_rules') && !addingRule && (
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() => {
                    setAddingRule(true)
                    setNewRuleName('')
                    setNewRuleVariation(variationNames[0] ?? '')
                    setDraftGroup(emptyGroup)
                  }}
                >
                  <Plus className="h-3.5 w-3.5" />
                  Add rule
                </Button>
              )}
            </div>
            <p className="mb-3 text-[13px] text-ink-muted">
              Checked top to bottom. The first match wins.
            </p>

            <div className="space-y-2.5">
              {rules.map((rule, i) => {
                const pct = pctFor(rule.name, rule.outcome)
                const isSplit = Boolean(rule.outcome?.percentage) || Boolean(draftPct[rule.name])
                const sum = Math.round(Object.values(pct).reduce((a, b) => a + b, 0) * 100) / 100
                const changed = Boolean(draftPct[rule.name])

                return (
                  <div key={rule.name || i} className="rounded-lg border p-3">
                    <div className="mb-2 flex items-center gap-2">
                      <Badge tone="brand">{i + 1}</Badge>
                      <span className="text-[13px] font-medium">{rule.name || 'Unnamed rule'}</span>
                      {rule.advanced && <Badge tone="warn">advanced</Badge>}
                      {rule.disabled && <Badge tone="neutral">off</Badge>}

                      {can('edit_rules') && (
                        <span className="ml-auto flex items-center gap-1">
                          <button
                            type="button"
                            aria-label={`Move ${rule.name} earlier`}
                            disabled={i === 0}
                            onClick={() => move(i, -1)}
                            className="h-7 w-7 rounded-md border text-ink-muted hover:text-ink disabled:opacity-30"
                          >
                            <ArrowUp className="mx-auto h-3.5 w-3.5" />
                          </button>
                          <button
                            type="button"
                            aria-label={`Move ${rule.name} later`}
                            disabled={i === rules.length - 1}
                            onClick={() => move(i, 1)}
                            className="h-7 w-7 rounded-md border text-ink-muted hover:text-ink disabled:opacity-30"
                          >
                            <ArrowDown className="mx-auto h-3.5 w-3.5" />
                          </button>
                          <button
                            type="button"
                            aria-label={`${rule.disabled ? 'Enable' : 'Disable'} ${rule.name}`}
                            onClick={() =>
                              void openReview({
                                kind: 'ruleState',
                                ruleName: rule.name,
                                disabled: !rule.disabled,
                              })
                            }
                            className="h-7 rounded-md border px-2 text-[11.5px] text-ink-muted hover:text-ink"
                          >
                            {rule.disabled ? 'Enable' : 'Disable'}
                          </button>
                          <button
                            type="button"
                            aria-label={`Delete rule ${rule.name}`}
                            onClick={() =>
                              void openReview({ kind: 'deleteRule', ruleName: rule.name })
                            }
                            className="h-7 w-7 rounded-md border text-ink-muted hover:border-danger hover:text-danger"
                          >
                            <Trash2 className="mx-auto h-3.5 w-3.5" />
                          </button>
                        </span>
                      )}
                    </div>

                    {editingRule === rule.name ? (
                      <div className="space-y-3">
                        <RuleBuilder
                          value={draftGroup}
                          onChange={setDraftGroup}
                          attributes={attributes ?? []}
                        />
                        <div className="flex gap-2">
                          <Button
                            size="sm"
                            aria-label={`Review conditions for ${rule.name}`}
                            disabled={!queryFromGroup(draftGroup)}
                            onClick={() =>
                              void openReview({
                                kind: 'rule',
                                ruleName: rule.name,
                                query: queryFromGroup(draftGroup),
                              })
                            }
                          >
                            Review change
                          </Button>
                          <Button size="sm" variant="outline" onClick={() => setEditingRule(null)}>
                            <X className="h-3.5 w-3.5" />
                            Cancel
                          </Button>
                        </div>
                      </div>
                    ) : (
                      <div className="flex items-start justify-between gap-2">
                        <p className="text-[13px] text-ink-soft">
                          {rule.advanced ? (
                            <>
                              Custom rule: <Code>{rule.query}</Code>
                            </>
                          ) : (
                            <ConditionView tokens={tokensFromCondition(rule.condition)} />
                          )}
                        </p>
                        {can('edit_rules') && (
                          <Button
                            size="sm"
                            variant="ghost"
                            aria-label={`Edit conditions for ${rule.name}`}
                            onClick={() => {
                              setEditingRule(rule.name)
                              setDraftGroup(groupFromCondition(rule.condition))
                            }}
                          >
                            <Pencil className="h-3.5 w-3.5" />
                            Edit
                          </Button>
                        )}
                      </div>
                    )}

                    <div className="mt-3 border-t pt-3">
                      {rule.progressive ? (
                        <ProgressiveEditor
                          rollout={rule.progressive}
                          variations={variationNames}
                          disabled={!can('rollout')}
                          onSave={(next) =>
                            void openReview({
                              kind: 'progressive',
                              ruleName: rule.name,
                              rollout: next,
                            })
                          }
                          onRemove={(variation) =>
                            void openReview({
                              kind: 'progressiveClear',
                              ruleName: rule.name,
                              variation,
                            })
                          }
                        />
                      ) : (
                        <>
                      {can('edit_rules') && (
                        <div className="mb-2 flex items-center gap-2">
                          <span className="text-[12px] text-ink-soft">Serves</span>
                          <div className="w-56">
                            <Select
                              ariaLabel={`What ${rule.name} serves`}
                              value={isSplit ? 'split' : 'single'}
                              onChange={(v) => {
                                if (v === 'split') {
                                  setDraftPct((prev) => ({ ...prev, [rule.name]: pct }))
                                } else {
                                  setDraftPct((prev) => {
                                    const next = { ...prev }
                                    delete next[rule.name]
                                    return next
                                  })
                                  void openReview({
                                    kind: 'outcome',
                                    ruleName: rule.name,
                                    outcome: { variation: variationNames[0] ?? '' },
                                  })
                                }
                              }}
                              options={[
                                { value: 'single', label: 'one variation' },
                                { value: 'split', label: 'a percentage split' },
                              ]}
                            />
                          </div>
                        </div>
                      )}

                      {isSplit ? (
                        <div className="space-y-2">
                          {variationNames.map((name) => (
                            <div key={name} className="flex items-center gap-3">
                              <span className="w-24 shrink-0 font-mono text-[12.5px] text-ink-soft">
                                {name}
                              </span>
                              <input
                                type="range"
                                min={0}
                                max={100}
                                step={1}
                                value={pct[name] ?? 0}
                                disabled={!can('rollout')}
                                aria-label={`${name} percentage`}
                                onChange={(e) =>
                                  setDraftPct((prev) => ({
                                    ...prev,
                                    [rule.name]: { ...pct, [name]: Number(e.target.value) },
                                  }))
                                }
                                className="flex-1 accent-[var(--color-brand)]"
                              />
                              <span className="w-12 text-right font-mono text-[12.5px]">
                                {pct[name] ?? 0}%
                              </span>
                            </div>
                          ))}
                          <div className="flex items-center justify-between pt-1">
                            <span className="text-[12px] text-ink-muted">
                              Weights, relative to each other. Total {sum}.
                            </span>
                            {changed && (
                              <Button
                                size="sm"
                                aria-label={`Review the split for ${rule.name}`}
                                onClick={() =>
                                  void openReview({
                                    kind: 'rollout',
                                    ruleName: rule.name,
                                    percentage: pct,
                                  })
                                }
                              >
                                Review change
                              </Button>
                            )}
                          </div>
                        </div>
                      ) : (
                        <p className="text-[13px] text-ink">
                          Serves <strong>{describeOutcome(rule.outcome)}</strong>
                        </p>
                      )}
                        </>
                      )}
                    </div>
                  </div>
                )
              })}

              {addingRule && (
                <div className="rounded-lg border border-brand p-3">
                  <p className="mb-2 text-[13px] font-medium">New rule</p>
                  <div className="mb-3 flex flex-wrap items-center gap-2">
                    <input
                      value={newRuleName}
                      onChange={(e) => setNewRuleName(e.target.value)}
                      placeholder="rule name"
                      aria-label="New rule name"
                      className="h-11 w-44 rounded-md border bg-surface px-2.5 text-sm focus:border-brand focus:outline-none"
                    />
                    <div className="w-56">
                      <Select
                        value={newRuleVariation}
                        onChange={(v) => setNewRuleVariation(v)}
                        ariaLabel="New rule serves"
                        options={variationNames.map((n) => ({ value: n, label: `serves ${n}` }))}
                      />
                    </div>
                  </div>

                  <RuleBuilder value={draftGroup} onChange={setDraftGroup} attributes={attributes ?? []} />

                  <div className="mt-3 flex gap-2">
                    <Button
                      size="sm"
                      aria-label="Review the new rule"
                      disabled={!newRuleName.trim() || !queryFromGroup(draftGroup)}
                      onClick={() =>
                        void openReview({
                          kind: 'addRule',
                          name: newRuleName.trim(),
                          query: queryFromGroup(draftGroup),
                          outcome: { variation: newRuleVariation },
                        })
                      }
                    >
                      Review change
                    </Button>
                    <Button size="sm" variant="outline" onClick={() => setAddingRule(false)}>
                      Cancel
                    </Button>
                  </div>
                </div>
              )}

              {rules.length === 0 && !addingRule && (
                <p className="rounded-lg border border-dashed px-3 py-4 text-center text-[13px] text-ink-muted">
                  No targeting rules yet.
                </p>
              )}

              <div className="rounded-lg border border-dashed p-3">
                <p className="text-[12px] font-medium uppercase tracking-wide text-ink-muted">
                  Default rule
                </p>
                <p className="mt-1 text-[13px] text-ink">
                  Serves <strong>{describeOutcome(flag.default)}</strong>
                  {rules.length === 0 ? ' to everyone' : ' when no rule above matches'}
                </p>
              </div>
            </div>
          </Card>
        </div>

        <div className="space-y-4">
          <Card className="p-6">
            <h2 className="mb-3 flex items-center gap-2 text-xl font-semibold tracking-tight text-ink">
              <Play className="h-4 w-4" />
              Preview
            </h2>
            <label htmlFor="preview-key" className="mb-1.5 block text-sm font-medium text-ink-soft">
              User ID
            </label>
            <Input
              id="preview-key"
              value={targetingKey}
              onChange={(e) => setTargetingKey(e.target.value)}
              className="h-11 text-base"
            />
            <label htmlFor="preview-attrs" className="mb-1.5 mt-4 block text-sm font-medium text-ink-soft">
              Context (JSON)
            </label>
            <textarea
              id="preview-attrs"
              value={attrsText}
              onChange={(e) => setAttrsText(e.target.value)}
              rows={4}
              className="w-full rounded-md border bg-surface px-3 py-2 font-mono text-sm focus:border-brand focus:outline-none"
            />
            <Button
              className="mt-4 w-full"
              onClick={() => void runPreview()}
              disabled={previewing}
            >
              {previewing ? <Spinner className="border-white/40 border-t-white" /> : null}
              Evaluate
            </Button>

            {preview && (
              <div className="mt-4 rounded-lg border border-[color:var(--color-brand)] bg-surface p-3">
                {preview.error ? (
                  <p className="text-sm text-danger">{preview.error}</p>
                ) : (
                  <>
                    <p className="text-base">
                      Gets <strong className="font-mono">{JSON.stringify(preview.value)}</strong>
                    </p>
                    <p className="mt-1 text-[13px] text-ink-muted">
                      {preview.variation && <>variation {preview.variation} · </>}
                      {preview.reason}
                    </p>
                  </>
                )}
              </div>
            )}
          </Card>

          {me?.capabilities?.history !== false && (
          <Card className="p-6">
            <h2 className="mb-3 flex items-center gap-2 text-xl font-semibold tracking-tight text-ink">
              <History className="h-4 w-4" />
              History
            </h2>
            {commits && commits.length > 0 ? (
              <ul className="space-y-3">
                {commits.slice(0, 8).map((c) => (
                  <li key={c.sha} className="border-b pb-2.5 last:border-0 last:pb-0">
                    <p className="text-sm text-ink">{c.message.split('\n')[0]}</p>
                    <p className="mt-1 text-[13px] text-ink-muted">
                      {c.author} · {new Date(c.when).toLocaleString()}
                    </p>
                  </li>
                ))}
              </ul>
            ) : (
              <p className="text-sm text-ink-muted">No changes recorded yet.</p>
            )}
          </Card>
          )}
        </div>
      </div>

      <ReviewDialog
        open={Boolean(pending)}
        onClose={() => setPending(null)}
        onConfirm={() => void confirm()}
        title="Review this change"
        diff={diff}
        loading={loadingDiff}
        saving={saving}
        protectedEnv={Boolean(environment?.protected)}
        envName={environment?.name ?? env}
        danger={pending?.kind === 'deleteFlag'}
      />
    </div>
  )
}

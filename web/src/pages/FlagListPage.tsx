import { useMemo, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { AlertTriangle, Plus, Search } from 'lucide-react'
import { api, ApiError, type DiffResult, type Environment, type Flag } from '@/lib/api'
import { useFlags, useSetState } from '@/hooks/useFlags'
import { Badge, Button, Card, Code, Input, Spinner, Toggle } from '@/components/ui/primitives'
import { ReviewDialog } from '@/components/ReviewDialog'
import { useToast } from '@/components/ui/Toast'
import { ScheduleBadge } from '@/components/ScheduleBadge'

function fileLabel(path: string) {
  const base = path.split('/').pop() ?? path
  return base.replace(/\.goff\.ya?ml$/, '').replace(/\.ya?ml$/, '')
}

const ALL = 'all'
const UNASSIGNED = 'unassigned'

function teamFilter(value: string) {
  return value === ALL || value === UNASSIGNED ? value : `team:${value}`
}

export function FlagListPage({ environments }: { environments: Environment[] }) {
  const { env = '' } = useParams()
  const environment = environments.find((e) => e.name === env)
  const { data, isLoading, error } = useFlags(env)
  const setState = useSetState(env)
  const toast = useToast()

  const [search, setSearch] = useState('')
  const [team, setTeam] = useState(ALL)
  const [pending, setPending] = useState<{ flag: Flag; enabled: boolean } | null>(null)
  const [diff, setDiff] = useState<DiffResult | undefined>()
  const [loadingDiff, setLoadingDiff] = useState(false)

  // Built from labels on the flags, not the destination list, so an odd label still filters.
  const options = useMemo(() => {
    const flags = data?.flags ?? []
    const labels = [...new Set(flags.map((f) => f.team).filter(Boolean))].sort()
    const unassigned = flags.some((f) => !f.team)
    return [
      { value: ALL, label: 'All teams' },
      ...labels.map((t) => ({ value: teamFilter(t), label: t })),
      ...(unassigned ? [{ value: UNASSIGNED, label: 'No team' }] : []),
    ]
  }, [data])

  const flags = useMemo(() => {
    const q = search.trim().toLowerCase()
    return (data?.flags ?? []).filter((f) => {
      if (team === UNASSIGNED && f.team) return false
      if (team !== ALL && team !== UNASSIGNED && teamFilter(f.team) !== team) return false
      if (!q) return true
      return f.key.toLowerCase().includes(q) || f.summary.toLowerCase().includes(q)
    })
  }, [data, search, team])

  async function requestToggle(flag: Flag, enabled: boolean) {
    if (!environment?.protected) {
      void commit(flag, enabled)
      return
    }
    setPending({ flag, enabled })
    setLoadingDiff(true)
    setDiff(undefined)
    try {
      setDiff(await api.diffState(env, flag.key, enabled))
    } catch (e) {
      toast(e instanceof Error ? e.message : 'Could not prepare the change', 'error')
      setPending(null)
    } finally {
      setLoadingDiff(false)
    }
  }

  async function commit(flag: Flag, enabled: boolean) {
    try {
      const result = await setState.mutateAsync({ flag, enabled })
      toast(result.message)
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

  if (isLoading) {
    return (
      <div className="flex items-center gap-2 text-ink-muted">
        <Spinner />
        <span>Loading flags…</span>
      </div>
    )
  }

  if (error) {
    return (
      <Card className="border-danger bg-danger-soft p-4 text-[13px] text-ink">
        {error instanceof Error ? error.message : 'Could not load flags'}
      </Card>
    )
  }

  return (
    <div className="space-y-5">
      <div className="flex items-baseline justify-between gap-4">
        <div>
          <h1 className="text-xl font-semibold tracking-tight">Feature flags</h1>
          <p className="mt-0.5 text-[13px] text-ink-muted">
            {data?.flags.length ?? 0} flags in {environment?.display ?? env}
          </p>
        </div>
        {(data?.teams ?? []).length > 0 && (
          <Link to={`/env/${env}/flags/new`}>
            <Button size="sm">
              <Plus className="h-3.5 w-3.5" />
              Create flag
            </Button>
          </Link>
        )}
      </div>

      {data?.broken && data.broken.length > 0 && (
        <Card className="border-warn bg-warn-soft p-4">
          <p className="flex items-center gap-2 text-[13px] font-medium text-warn">
            <AlertTriangle className="h-4 w-4" />
            {data.broken.length} flag{data.broken.length === 1 ? '' : 's'} need attention
          </p>
          <ul className="mt-2 space-y-1 text-[12.5px] text-ink">
            {data.broken.map((b) => (
              <li key={`${b.file}:${b.key}`}>
                <Code>{b.key}</Code> in <Code>{fileLabel(b.file)}</Code> — {b.reason}
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
            placeholder="Search flags"
            className="pl-8"
            aria-label="Search flags"
          />
        </div>
        <select
          value={team}
          onChange={(e) => setTeam(e.target.value)}
          aria-label="Filter by team"
          className="h-9 rounded-md border bg-surface px-2.5 text-sm text-ink focus:border-brand focus:outline-none"
        >
          {options.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
        <span className="ml-auto text-[13px] text-ink-muted">
          {flags.length} shown
        </span>
      </div>

      <Card className="overflow-hidden">
        <table className="w-full">
          <thead className="bg-canvas">
            <tr className="border-b text-left text-[11px] font-medium uppercase tracking-wide text-ink-muted">
              <th className="px-4 py-2.5">Flag</th>
              <th className="px-4 py-2.5">Team</th>
              <th className="w-20 px-4 py-2.5 text-right">On</th>
            </tr>
          </thead>
          <tbody>
            {flags.map((flag) => {
              const canToggle = flag.actions.includes('toggle')
              const busy = setState.isPending && setState.variables?.flag.key === flag.key
              return (
                <tr key={flag.key} className="border-b last:border-0 hover:bg-canvas/60">
                  <td className="px-4 py-3 align-top">
                    <Link
                      to={`/env/${env}/flags/${encodeURIComponent(flag.key)}`}
                      className="font-mono text-[13px] font-medium text-brand hover:underline"
                    >
                      {flag.key}
                    </Link>
                    {(flag.preserved?.length ?? 0) > 0 && (
                      <Badge tone="neutral" className="ml-2">
                        advanced fields
                      </Badge>
                    )}
                    <ScheduleBadge flag={flag} className="ml-2" />
                  </td>
                  <td className="px-4 py-3 align-top">
                    {flag.team ? (
                      <Badge tone="neutral" title={flag.file}>
                        {flag.team}
                      </Badge>
                    ) : (
                      <span className="text-[13px] text-ink-muted" title={flag.file}>
                        —
                      </span>
                    )}
                  </td>
                  <td className="px-4 py-3 text-right align-top">
                    <div className="flex items-center justify-end gap-2">
                      {busy && <Spinner />}
                      <Toggle
                        checked={flag.enabled}
                        disabled={!canToggle}
                        busy={busy}
                        label={`Turn ${flag.key} ${flag.enabled ? 'off' : 'on'}`}
                        onChange={(next) => void requestToggle(flag, next)}
                      />
                    </div>
                  </td>
                </tr>
              )
            })}
            {flags.length === 0 && (
              <tr>
                <td colSpan={4} className="px-4 py-12 text-center text-[13px] text-ink-muted">
                  No flags match
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </Card>

      <ReviewDialog
        open={Boolean(pending)}
        onClose={() => setPending(null)}
        onConfirm={() => pending && void commit(pending.flag, pending.enabled)}
        title="Review this change"
        diff={diff}
        loading={loadingDiff}
        saving={setState.isPending}
        protectedEnv={Boolean(environment?.protected)}
        envName={environment?.name ?? env}
      />
    </div>
  )
}

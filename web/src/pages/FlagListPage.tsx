import { useMemo, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { AlertTriangle, ChevronDown, ChevronsUpDown, ChevronUp, ExternalLink, Plus, Search } from 'lucide-react'
import { type Environment, type Flag } from '@/lib/api'
import { useFlags } from '@/hooks/useFlags'
import { Badge, Button, Card, Code, Input, Spinner } from '@/components/ui/primitives'
import { ScheduleBadge } from '@/components/ScheduleBadge'

function fileLabel(path: string) {
  const base = path.split('/').pop() ?? path
  return base.replace(/\.goff\.ya?ml$/, '').replace(/\.ya?ml$/, '')
}

function metaDate(flag: Flag, field: 'createdAt' | 'updatedAt'): Date | null {
  const raw = flag.metadata?.[field]
  if (typeof raw !== 'string') return null
  const d = new Date(raw)
  return Number.isNaN(d.getTime()) ? null : d
}

function DateCell({ date }: { date: Date | null }) {
  if (!date) return <span className="text-sm text-ink-muted">—</span>
  return (
    <span className="font-mono text-sm text-ink" title={date.toLocaleString()}>
      {relativeTime(date)}
    </span>
  )
}

function relativeTime(d: Date): string {
  const diffMs = Date.now() - d.getTime()
  const days = Math.floor(diffMs / 86400000)
  if (days < 1) return 'today'
  if (days < 7) return `${days}d ago`
  if (days < 30) return `${Math.floor(days / 7)}w ago`
  if (days < 365) return `${Math.floor(days / 30)}mo ago`
  return `${Math.floor(days / 365)}y ago`
}

export function FlagListPage({ environments: _environments }: { environments: Environment[] }) {
  const { env = '' } = useParams()
  const navigate = useNavigate()
  const { data, isLoading, error } = useFlags(env)

  const [search, setSearch] = useState('')
  const [sort, setSort] = useState<{
    col: 'key' | 'team' | 'enabled' | 'created' | 'updated'
    dir: 'asc' | 'desc'
  } | null>(null)

  const toggleSort = (col: 'key' | 'team' | 'enabled' | 'created' | 'updated') =>
    setSort((s) =>
      s?.col !== col ? { col, dir: 'asc' } : s.dir === 'asc' ? { col, dir: 'desc' } : null,
    )

  const flags = useMemo(() => {
    const q = search.trim().toLowerCase()
    const filtered = (data?.flags ?? []).filter((f) => {
      if (!q) return true
      return (
        f.key.toLowerCase().includes(q) ||
        f.summary.toLowerCase().includes(q) ||
        (f.team ?? '').toLowerCase().includes(q)
      )
    })
    if (!sort) return filtered
    const dir = sort.dir === 'asc' ? 1 : -1
    return [...filtered].sort((a, b) => {
      if (sort.col === 'enabled') return (Number(a.enabled) - Number(b.enabled)) * dir
      if (sort.col === 'created' || sort.col === 'updated') {
        const field = sort.col === 'created' ? 'createdAt' : 'updatedAt'
        const ta = metaDate(a, field)?.getTime()
        const tb = metaDate(b, field)?.getTime()
        if (ta === tb) return 0
        if (ta === undefined) return 1
        if (tb === undefined) return -1
        return (ta - tb) * dir
      }
      const va = String(a[sort.col] ?? '').toLowerCase()
      const vb = String(b[sort.col] ?? '').toLowerCase()
      return va === vb ? 0 : va < vb ? -1 * dir : 1 * dir
    })
  }, [data, search, sort])

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
      <h1 className="text-4xl font-bold tracking-tight max-md:text-3xl">Feature flags</h1>

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

      <div className="flex flex-wrap items-center gap-3">
        <div className="relative w-72 max-md:w-full">
          <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-ink-muted" />
          <Input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search flags, teams…"
            className="h-9 pl-9 text-sm max-md:h-11"
            enterKeyHint="search"
            aria-label="Search flags"
          />
        </div>
        <div className="ml-auto max-md:ml-0 max-md:w-full">
          {(data?.canCreate || (data?.teams ?? []).length > 0) && (
            <Link to={`/env/${env}/flags/new`} className="max-md:block">
              <Button size="sm" className="max-md:w-full max-md:text-sm">
                <Plus className="h-3.5 w-3.5" />
                Create flag
              </Button>
            </Link>
          )}
        </div>
      </div>

      <div className="w-full">
        <table className="w-full">
          <thead>
            <tr className="border-b border-[color:var(--color-line)] text-xl font-semibold tracking-tight text-ink max-md:text-base">
              <SortableHeader
                label="Flag"
                col="key"
                sort={sort}
                onClick={() => toggleSort('key')}
                className="px-4 pb-3 pt-4 text-left max-md:pl-1 max-md:pr-2"
              />
              <SortableHeader
                label="Team"
                col="team"
                sort={sort}
                onClick={() => toggleSort('team')}
                className="px-4 pb-3 pt-4 text-left max-md:px-2"
              />
              <SortableHeader
                label="Created"
                col="created"
                sort={sort}
                onClick={() => toggleSort('created')}
                className="w-32 px-4 pb-3 pt-4 text-center max-md:hidden"
                justify="center"
              />
              <SortableHeader
                label="Updated"
                col="updated"
                sort={sort}
                onClick={() => toggleSort('updated')}
                className="w-32 px-4 pb-3 pt-4 text-center max-md:hidden"
                justify="center"
              />
              <SortableHeader
                label="Serving"
                col="enabled"
                sort={sort}
                onClick={() => toggleSort('enabled')}
                className="w-24 px-4 pb-3 pt-4 text-center max-md:w-auto max-md:px-2"
                justify="center"
              />
              <th className="w-10 px-2 pb-3 pt-4 max-md:hidden" aria-hidden />
            </tr>
          </thead>
          <tbody className="divide-y divide-[color:var(--color-line)]">
            {flags.map((flag) => {
              const href = `/env/${env}/flags/${encodeURIComponent(flag.key)}`
              return (
                <tr
                  key={flag.key}
                  role="link"
                  tabIndex={0}
                  aria-label={`Open ${flag.key}`}
                  onClick={(e) => {
                    // Ignore clicks that originated from a link/button inside the row.
                    if ((e.target as HTMLElement).closest('a, button, input, select, textarea')) return
                    navigate(href)
                  }}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' || e.key === ' ') {
                      if ((e.target as HTMLElement).closest('a, button, input, select, textarea')) return
                      e.preventDefault()
                      navigate(href)
                    }
                  }}
                  className="cursor-pointer transition-colors hover:bg-[color:var(--color-row-hover)] focus:bg-[color:var(--color-row-hover)] focus:outline-none"
                >
                  <td className="px-4 py-3 align-top max-md:py-3.5 max-md:pl-1 max-md:pr-2">
                    <div className="flex items-center gap-2 max-md:flex-wrap max-md:gap-1.5">
                      <span className="font-mono text-[15px] font-medium text-ink max-md:wrap-anywhere">
                        {flag.key}
                      </span>
                      {(flag.preserved?.length ?? 0) > 0 && (
                        <Badge tone="neutral">advanced fields</Badge>
                      )}
                      <ScheduleBadge flag={flag} />
                    </div>
                    {flag.summary && (
                      <p className="mt-1 text-[12.5px] text-ink-muted">{flag.summary}</p>
                    )}
                  </td>
                  <td className="px-4 py-3 align-top max-md:px-2 max-md:py-3.5 max-md:break-words">
                    {flag.team ? (
                      <span className="text-sm text-ink" title={flag.file}>
                        {flag.team}
                      </span>
                    ) : (
                      <span className="text-sm text-ink-muted" title={flag.file}>
                        —
                      </span>
                    )}
                  </td>
                  <td className="w-32 px-4 py-3 text-center align-top max-md:hidden">
                    <DateCell date={metaDate(flag, 'createdAt')} />
                  </td>
                  <td className="w-32 px-4 py-3 text-center align-top max-md:hidden">
                    <DateCell date={metaDate(flag, 'updatedAt')} />
                  </td>
                  <td className="w-24 px-4 py-3 text-center align-top max-md:w-auto max-md:px-2 max-md:py-3.5">
                    <span
                      className={`font-mono text-base font-semibold ${
                        flag.enabled ? 'text-brand' : 'text-ink-muted'
                      }`}
                    >
                      {flag.enabled ? 'true' : 'false'}
                    </span>
                  </td>
                  <td className="w-10 px-2 py-3 text-right align-top max-md:hidden">
                    <Link
                      to={`/env/${env}/flags/${encodeURIComponent(flag.key)}`}
                      aria-label={`Open ${flag.key}`}
                      className="inline-flex h-8 w-8 items-center justify-center rounded-md text-ink-muted transition-colors hover:bg-canvas hover:text-brand"
                    >
                      <ExternalLink className="h-4 w-4" />
                    </Link>
                  </td>
                </tr>
              )
            })}
            {flags.length === 0 && (
              <tr>
                <td colSpan={6} className="px-4 py-12 text-center text-[13px] text-ink-muted">
                  No flags match
                </td>
              </tr>
            )}
          </tbody>
        </table>

        {flags.length > 0 && (
          <div className="mt-1 flex items-center justify-between px-4 py-3 text-[13px] text-ink-muted max-md:px-1">
            <span>
              {flags.length} of {data?.flags.length ?? 0} item{(data?.flags.length ?? 0) === 1 ? '' : 's'}
            </span>
            <span>1 of 1 pages</span>
          </div>
        )}
      </div>
    </div>
  )
}

type SortState = { col: 'key' | 'team' | 'enabled' | 'created' | 'updated'; dir: 'asc' | 'desc' } | null

function SortableHeader({
  label,
  col,
  sort,
  onClick,
  className,
  justify = 'start',
}: {
  label: string
  col: 'key' | 'team' | 'enabled' | 'created' | 'updated'
  sort: SortState
  onClick: () => void
  className?: string
  justify?: 'start' | 'end' | 'center'
}) {
  const active = sort?.col === col
  const Icon = !active ? ChevronsUpDown : sort!.dir === 'asc' ? ChevronUp : ChevronDown
  const justifyClass =
    justify === 'end' ? 'justify-end' : justify === 'center' ? 'justify-center w-full' : 'justify-start'
  return (
    <th className={className}>
      <button
        type="button"
        onClick={onClick}
        className={`inline-flex items-center gap-1.5 rounded-sm font-semibold tracking-tight transition-colors hover:text-brand max-md:min-h-11 max-md:gap-1 ${justifyClass} ${active ? 'text-brand' : ''}`}
      >
        <span>{label}</span>
        <Icon className={`h-4 w-4 ${active ? 'opacity-100' : 'opacity-40'}`} />
      </button>
    </th>
  )
}

import { Link, useParams } from 'react-router-dom'
import { Users } from 'lucide-react'
import { useTeams } from '@/hooks/useFlags'
import type { TeamSummary } from '@/lib/api'
import { Badge, Card, Spinner } from '@/components/ui/primitives'

function teamGroups(team: TeamSummary) {
  return team.groups.filter((g) => !g.allTeams)
}

export function TeamsPage() {
  const { env = '' } = useParams()
  const { data, isLoading, error } = useTeams()

  if (isLoading) {
    return (
      <div className="flex items-center gap-2 text-ink-muted">
        <Spinner />
        <span>Loading teams…</span>
      </div>
    )
  }

  if (error) {
    return (
      <Card className="border-danger bg-danger-soft p-4 text-[13px] text-ink">
        {error instanceof Error ? error.message : 'Could not load teams'}
      </Card>
    )
  }

  const teams = data?.teams ?? []
  const environments = [...new Set(teams.flatMap((t) => t.environments.map((e) => e.name)))]

  return (
    <div className="space-y-5">
      <h1 className="text-4xl font-bold tracking-tight max-md:text-3xl">Teams</h1>
      <p className="text-[13px] text-ink-soft">Teams are set in Studio&apos;s config file. Ask an administrator to add one.</p>

      {teams.length === 0 ? (
        <Card className="flex items-center gap-2 p-6 text-[13px] text-ink-muted">
          <Users className="h-4 w-4" />
          There are no teams you can see.
        </Card>
      ) : (
        <div className="w-full overflow-x-auto">
          <table className="w-full">
            <thead>
              <tr className="border-b border-[color:var(--color-line)] text-left text-xl font-semibold tracking-tight text-ink max-md:text-base">
                <th className="px-4 pb-3 pt-4 max-md:pl-1 max-md:pr-2">Team</th>
                <th className="px-4 pb-3 pt-4 max-md:px-2">Editors</th>
                {environments.map((name) => (
                  <th key={name} className="w-28 px-4 pb-3 pt-4 text-center max-md:px-2">
                    {name}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody className="divide-y divide-[color:var(--color-line)]">
              {teams.map((team) => (
                <tr key={team.name} aria-label={team.name}>
                  <td className="px-4 py-3 align-top max-md:pl-1 max-md:pr-2">
                    <span className="font-mono text-[15px] font-medium text-ink">{team.name}</span>
                    {team.canEdit && (
                      <Badge tone="brand" className="ml-2">
                        you edit
                      </Badge>
                    )}
                  </td>
                  <td className="px-4 py-3 align-top text-sm text-ink max-md:px-2">
                    {teamGroups(team).length === 0 ? (
                      <span className="text-ink-muted">—</span>
                    ) : (
                      <ul className="space-y-0.5">
                        {teamGroups(team).map((g) => (
                          <li key={g.name}>
                            {g.name}
                            {!g.edits && <span className="ml-1 text-[12px] text-ink-muted">(view)</span>}
                          </li>
                        ))}
                      </ul>
                    )}
                  </td>
                  {environments.map((name) => {
                    const e = team.environments.find((x) => x.name === name)
                    return (
                      <td key={name} className="px-4 py-3 text-center align-top max-md:px-2">
                        {e ? (
                          <Link
                            to={`/env/${name}?q=${encodeURIComponent(team.name)}`}
                            aria-label={`${e.flags} ${team.name} flags in ${name}`}
                            className={
                              name === env
                                ? 'font-mono text-base font-semibold text-brand hover:underline'
                                : 'font-mono text-base text-ink hover:underline'
                            }
                          >
                            {e.flags}
                          </Link>
                        ) : (
                          <span className="text-ink-muted">—</span>
                        )}
                      </td>
                    )
                  })}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

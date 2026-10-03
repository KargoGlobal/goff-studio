import type { TeamOption } from '@/lib/api'

export const NO_TEAM = 'No team'

export function teamLabel(name: string): string {
  return name === '' ? NO_TEAM : name
}

export function teamChoices(teams: TeamOption[] | null | undefined): { value: string; label: string }[] {
  return (teams ?? []).map((t) => ({ value: t.name, label: teamLabel(t.name) }))
}

export function defaultTeam(teams: TeamOption[] | null | undefined): string | undefined {
  return teams?.[0]?.name
}

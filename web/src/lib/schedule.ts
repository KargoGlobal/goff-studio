import type { Experimentation, Flag } from '@/lib/api'
import { formatWhen } from '@/lib/dates'

export type EffectiveState = 'on' | 'off' | 'scheduled' | 'expired'

function bound(iso?: string): number | null {
  if (!iso) return null
  const ms = new Date(iso).getTime()
  return Number.isNaN(ms) ? null : ms
}

// Mirrors GOFF's isExperimentationOver: outside the window the flag serves the default.
function isWindowClosed(window?: Experimentation, now: Date = new Date()): boolean {
  if (!window) return false
  const at = now.getTime()
  const start = bound(window.start)
  const end = bound(window.end)
  return (start !== null && at < start) || (end !== null && at > end)
}

export function effectiveState(
  flag: Pick<Flag, 'enabled' | 'experimentation'>,
  now: Date = new Date(),
): EffectiveState {
  if (!flag.enabled) return 'off'
  if (!isWindowClosed(flag.experimentation, now)) return 'on'
  const start = bound(flag.experimentation?.start)
  return start !== null && now.getTime() < start ? 'scheduled' : 'expired'
}

export function describeEffectiveState(state: EffectiveState, window?: Experimentation): string {
  switch (state) {
    case 'scheduled':
      return window?.start
        ? `Off until ${formatWhen(window.start)}, then on`
        : 'Off until the window opens'
    case 'expired':
      return window?.end ? `Off since ${formatWhen(window.end)}` : 'Off, the window has closed'
    case 'off':
      return 'Off for everyone'
    case 'on':
      return 'On'
  }
}

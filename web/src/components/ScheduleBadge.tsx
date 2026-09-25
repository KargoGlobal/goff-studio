import type { Flag } from '@/lib/api'
import { Badge } from '@/components/ui/primitives'
import { describeEffectiveState, effectiveState } from '@/lib/schedule'

export function ScheduleBadge({
  flag,
  className,
}: {
  flag: Pick<Flag, 'enabled' | 'experimentation'>
  className?: string
}) {
  const state = effectiveState(flag)
  if (state !== 'scheduled' && state !== 'expired') return null
  return (
    <Badge tone="warn" className={className} title={describeEffectiveState(state, flag.experimentation)}>
      {state}
    </Badge>
  )
}

import type { Condition, Flag, Outcome, Rule } from '@/lib/api'

const splits = (o: Outcome | undefined) => Object.keys(o?.percentage ?? {}).length > 0

export function hasSplit(flag: Pick<Flag, 'default' | 'rules'>): boolean {
  return splits(flag.default) || (flag.rules ?? []).some((r) => !r.disabled && (splits(r.outcome) || Boolean(r.progressive)))
}

function pins(c: Condition | undefined, attribute: string): boolean {
  if (!c || c.not) return false
  if (c.children) return c.op !== 'or' && c.children.some((child) => pins(child, attribute))
  if (c.attribute !== attribute) return false
  if (c.operator === 'eq') return true
  return c.operator === 'in' && (c.value ?? '').split(',').filter((v) => v.trim()).length === 1
}

export function rulesSplittingOneValue(flag: Pick<Flag, 'rules'>, attribute: string): Rule[] {
  if (!attribute) return []
  return (flag.rules ?? []).filter(
    (r) => !r.disabled && (splits(r.outcome) || Boolean(r.progressive)) && pins(r.condition, attribute),
  )
}

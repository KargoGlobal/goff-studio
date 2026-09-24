import type { Condition, Outcome } from '@/lib/api'

const OPERATOR_LABELS: Record<string, string> = {
  eq: 'equals',
  ne: 'does not equal',
  co: 'contains',
  sw: 'starts with',
  ew: 'ends with',
  in: 'is one of',
  gt: 'is greater than',
  ge: 'is greater than or equal to',
  lt: 'is less than',
  le: 'is less than or equal to',
  pr: 'is present',
}

export function describeOutcome(outcome: Outcome): string {
  if (outcome.percentage && Object.keys(outcome.percentage).length > 0) {
    return Object.entries(outcome.percentage)
      .sort((a, b) => b[1] - a[1])
      .map(([name, pct]) => `${pct}% ${name}`)
      .join(' / ')
  }
  return outcome.variation || 'nothing'
}

export function describeCondition(condition?: Condition): string {
  if (!condition) return 'anyone'

  if (condition.op) {
    const parts = (condition.children ?? []).map(describeCondition).filter(Boolean)
    if (parts.length === 0) return 'anyone'
    if (parts.length === 1) return parts[0]
    return parts.join(condition.op === 'or' ? ' or ' : ' and ')
  }

  const label = OPERATOR_LABELS[condition.operator ?? ''] ?? condition.operator ?? ''
  if (condition.operator === 'pr') return `${condition.attribute} ${label}`

  const value = (condition.value ?? '')
    .split(',')
    .map((v) => v.trim())
    .filter(Boolean)

  const rendered = value.length > 1 ? `${value.slice(0, -1).join(', ')} or ${value.at(-1)}` : condition.value

  return `${condition.attribute} ${label} ${rendered}`
}

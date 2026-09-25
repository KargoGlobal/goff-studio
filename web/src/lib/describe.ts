import type { Outcome } from '@/lib/api'

export function describeOutcome(outcome: Outcome): string {
  if (outcome.percentage && Object.keys(outcome.percentage).length > 0) {
    return Object.entries(outcome.percentage)
      .sort((a, b) => b[1] - a[1])
      .map(([name, pct]) => `${pct}% ${name}`)
      .join(' / ')
  }
  return outcome.variation || 'nothing'
}

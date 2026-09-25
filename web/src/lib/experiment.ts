import type { Allocation, Arm, ShardRange } from '@/lib/api'

export interface ArmShare {
  variation: string
  /** Share of exposed subjects, 0..1. */
  ofExposed: number
  /** Share of everyone matching the rule, 0..1. */
  ofAll: number
}

export interface AllocationSummary {
  /** Percent of matching subjects who are exposed, rounded to 0.01. */
  exposurePercent: number
  arms: ArmShare[]
  /** True when exposure sits on its own salt, so it can be ramped safely. */
  rampable: boolean
}

function covered(ranges: ShardRange[], total: number): number {
  const sorted = ranges
    .map(([s, e]) => [Math.max(0, s), Math.min(total, e)] as ShardRange)
    .filter(([s, e]) => s < e)
    .sort((a, b) => a[0] - b[0])
  let n = 0
  let end = -1
  for (const [s, e] of sorted) {
    const start = Math.max(s, end)
    if (e > start) n += e - start
    end = Math.max(end, e)
  }
  return n
}

function intersect(a: ShardRange[], b: ShardRange[]): ShardRange[] {
  const out: ShardRange[] = []
  for (const [s1, e1] of a)
    for (const [s2, e2] of b) {
      const s = Math.max(s1, s2)
      const e = Math.min(e1, e2)
      if (s < e) out.push([s, e])
    }
  return out
}

const round2 = (n: number) => Math.round(n * 100) / 100

function sameRanges(a: ShardRange[], b: ShardRange[]) {
  return a.length === b.length && a.every(([s, e], i) => s === b[i][0] && e === b[i][1])
}

function exposureSalt(a: Allocation): string | null {
  for (const idx of [0, 1]) {
    const first = a.splits[0]?.shards[idx]
    if (!first) return null
    const ok = a.splits.every(
      (s) =>
        s.shards.length === 2 &&
        s.shards[idx].salt === first.salt &&
        sameRanges(s.shards[idx].ranges, first.ranges) &&
        s.shards[1 - idx].salt !== first.salt,
    )
    if (ok) return first.salt
  }
  return null
}

/** Mirrors pkg/splits Shares: different salts are independent, one salt intersects. */
export function summarizeAllocation(a: Allocation, totalShards: number): AllocationSummary {
  const ofAll = a.splits.map((split) => {
    const bySalt = new Map<string, ShardRange[]>()
    for (const shard of split.shards) {
      const prev = bySalt.get(shard.salt)
      bySalt.set(shard.salt, prev ? intersect(prev, shard.ranges) : shard.ranges)
    }
    let share = 1
    for (const ranges of bySalt.values()) share *= covered(ranges, totalShards) / totalShards
    return share
  })
  const exposed = ofAll.reduce((sum, s) => sum + s, 0)
  return {
    exposurePercent: round2(exposed * 100),
    rampable: exposureSalt(a) !== null,
    arms: a.splits.map((s, i) => ({
      variation: s.variation,
      ofAll: ofAll[i],
      ofExposed: exposed > 0 ? ofAll[i] / exposed : 0,
    })),
  }
}

/** Smallest step the shard count allows, e.g. 0.01 for 10,000 shards. */
export function exposureStep(totalShards: number): number {
  return 100 / totalShards
}

/**
 * Exposure can only grow: shrinking would drop exposed subjects out of their
 * arm. Lowering it is only allowed as part of a re-randomization.
 */
export function clampExposure(next: number, current: number, rerandomize: boolean): number {
  const bounded = Math.min(100, Math.max(0, round2(next)))
  if (rerandomize) return Math.max(bounded, 0.01)
  return Math.max(bounded, current)
}

export type ExposureIntent = 'none' | 'grow' | 'rerandomize'

export function exposureIntent(next: number, current: number, rerandomize: boolean): ExposureIntent {
  if (rerandomize) return 'rerandomize'
  return next > current ? 'grow' : 'none'
}

export function armsFromWeights(weights: Record<string, number>): Arm[] {
  return Object.entries(weights)
    .filter(([, w]) => w > 0)
    .map(([variation, weight]) => ({ variation, weight }))
}

export function formatPercent(fraction: number): string {
  const pct = round2(fraction * 100)
  return `${pct}%`
}

export type WindowState = 'not-started' | 'running' | 'ended' | 'always'

export function windowState(a: Allocation, now: Date = new Date()): WindowState {
  const start = a.startAt ? new Date(a.startAt) : null
  const end = a.endAt ? new Date(a.endAt) : null
  if (!start && !end) return 'always'
  if (start && now < start) return 'not-started'
  if (end && now > end) return 'ended'
  return 'running'
}

/** A plain-language line for the preview result of an experiment flag. */
export function describeReason(reason: string): string {
  switch (reason) {
    case 'SPLIT':
      return 'In the experiment'
    case 'PASS_THROUGH':
      return 'Matched an experiment rule but was not exposed, so it continued down the rules'
    case 'HOLDOUT':
      return 'In the holdout, so it gets the default and is logged as holdout'
    case 'OUTSIDE_WINDOW':
      return 'Matched an experiment rule outside its window'
    case 'STOCK':
      return "Served a rule's own variation"
    case 'DEFAULT':
      return 'No rule matched, so it gets the default'
    case 'DISABLED':
      return 'The flag is off'
    default:
      return reason
  }
}

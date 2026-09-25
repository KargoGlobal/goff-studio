import { describe, expect, it } from 'vitest'
import type { Allocation } from './api'
import {
  armsFromWeights,
  clampExposure,
  describeReason,
  exposureIntent,
  exposureStep,
  summarizeAllocation,
  windowState,
} from './experiment'

function twoSalt(exposureEnd: number, cuts: number[]): Allocation {
  const edges = [0, ...cuts, 10000]
  return {
    startAt: null,
    endAt: null,
    layer: null,
    splits: edges.slice(0, -1).map((start, i) => ({
      variation: `arm-${i}`,
      shards: [
        { salt: 'exp', ranges: [[0, exposureEnd]] },
        { salt: 'arm', ranges: [[start, edges[i + 1]]] },
      ],
    })),
  }
}

describe('summarizeAllocation', () => {
  it('reads exposure and arm shares from a two-salt layout', () => {
    const got = summarizeAllocation(twoSalt(100, [3334, 6667]), 10000)
    expect(got.exposurePercent).toBe(1)
    expect(got.rampable).toBe(true)
    expect(got.arms.map((a) => Math.round(a.ofExposed * 10000) / 100)).toEqual([33.34, 33.33, 33.33])
  })

  it('treats a single shared salt as disjoint slices, not a ramp', () => {
    const a: Allocation = {
      startAt: null,
      endAt: null,
      layer: null,
      splits: [
        { variation: 'a', shards: [{ salt: 's', ranges: [[0, 5000]] }] },
        { variation: 'b', shards: [{ salt: 's', ranges: [[5000, 10000]] }] },
      ],
    }
    const got = summarizeAllocation(a, 10000)
    expect(got.exposurePercent).toBe(100)
    expect(got.rampable).toBe(false)
    expect(got.arms[0].ofExposed).toBe(0.5)
  })

  it('counts overlapping ranges once', () => {
    const a = twoSalt(100, [5000])
    a.splits[0].shards[0].ranges = [
      [0, 60],
      [40, 100],
    ]
    a.splits[1].shards[0].ranges = a.splits[0].shards[0].ranges
    expect(summarizeAllocation(a, 10000).exposurePercent).toBe(1)
  })
})

describe('clampExposure', () => {
  it('never lets exposure shrink without re-randomizing', () => {
    expect(clampExposure(0.5, 1, false)).toBe(1)
    expect(clampExposure(2, 1, false)).toBe(2)
    expect(clampExposure(150, 1, false)).toBe(100)
  })

  it('allows lowering exposure when re-randomizing, but not to zero', () => {
    expect(clampExposure(0.5, 1, true)).toBe(0.5)
    expect(clampExposure(0, 1, true)).toBe(0.01)
  })

  it('rounds to the 0.01% shard precision', () => {
    expect(clampExposure(1.23456, 1, false)).toBe(1.23)
    expect(exposureStep(10000)).toBe(0.01)
  })
})

describe('exposureIntent', () => {
  it('distinguishes a ramp from a no-op and a reshuffle', () => {
    expect(exposureIntent(2, 1, false)).toBe('grow')
    expect(exposureIntent(1, 1, false)).toBe('none')
    expect(exposureIntent(1, 1, true)).toBe('rerandomize')
  })
})

describe('armsFromWeights', () => {
  it('drops zero-weight variations', () => {
    expect(armsFromWeights({ control: 50, treatment: 50, other: 0 })).toEqual([
      { variation: 'control', weight: 50 },
      { variation: 'treatment', weight: 50 },
    ])
  })
})

describe('windowState', () => {
  const at = new Date('2026-10-15T00:00:00Z')
  const base = twoSalt(100, [5000])
  it('reports each phase of the window', () => {
    expect(windowState(base, at)).toBe('always')
    expect(windowState({ ...base, startAt: '2026-11-01T00:00:00Z' }, at)).toBe('not-started')
    expect(windowState({ ...base, endAt: '2026-10-01T00:00:00Z' }, at)).toBe('ended')
    expect(
      windowState({ ...base, startAt: '2026-10-01T00:00:00Z', endAt: '2026-11-01T00:00:00Z' }, at),
    ).toBe('running')
  })
})

describe('describeReason', () => {
  it('explains pass-through in plain language', () => {
    expect(describeReason('PASS_THROUGH')).toMatch(/not exposed/)
    expect(describeReason('SOMETHING_NEW')).toBe('SOMETHING_NEW')
  })
})

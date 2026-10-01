import { describe, expect, it } from 'vitest'
import { normalizeSplit, setShare, sumsTo100 } from './split'

const names = ['on', 'off']

describe('setShare', () => {
  it('moves the other variation so the total stays 100', () => {
    expect(setShare(names, { on: 20, off: 80 }, 'on', 55)).toEqual({ on: 55, off: 45 })
  })

  it('cannot set 100 on two variations', () => {
    const once = setShare(names, { on: 50, off: 50 }, 'on', 100)
    expect(setShare(names, once, 'off', 100)).toEqual({ on: 0, off: 100 })
  })

  it('shares the rest in proportion across three variations', () => {
    const three = ['a', 'b', 'c']
    expect(setShare(three, { a: 40, b: 40, c: 20 }, 'a', 70)).toEqual({ a: 70, b: 20, c: 10 })
  })

  it('shares the rest evenly when the others are all zero', () => {
    const three = ['a', 'b', 'c']
    const out = setShare(three, { a: 100, b: 0, c: 0 }, 'a', 0)
    expect(out.a).toBe(0)
    expect(out.b + out.c).toBe(100)
  })

  it('clamps and rounds', () => {
    expect(setShare(names, { on: 50, off: 50 }, 'on', 140)).toEqual({ on: 100, off: 0 })
    expect(setShare(names, { on: 50, off: 50 }, 'on', 33.6)).toEqual({ on: 34, off: 66 })
  })
})

describe('normalizeSplit', () => {
  it('shows hand-written weights as their real share', () => {
    expect(normalizeSplit(names, { on: 10, off: 10 })).toEqual({ on: 50, off: 50 })
    expect(normalizeSplit(names, { on: 30, off: 10 })).toEqual({ on: 75, off: 25 })
  })

  it('leaves a split that already totals 100 alone, fractions included', () => {
    expect(normalizeSplit(names, { on: 0.5, off: 99.5 })).toEqual({ on: 0.5, off: 99.5 })
  })

  it('fills variations missing from the file with zero', () => {
    expect(normalizeSplit(['on', 'off', 'beta'], { on: 100 })).toEqual({ on: 100, off: 0, beta: 0 })
  })
})

describe('sumsTo100', () => {
  it('spots weights that are not percentages', () => {
    expect(sumsTo100({ on: 100, off: 100 })).toBe(false)
    expect(sumsTo100({ on: 20, off: 80 })).toBe(true)
  })
})

import { describe, expect, it } from 'vitest'
import type { Rule } from './api'
import { hasSplit, rulesSplittingOneValue } from './bucketing'

const rule = (over: Partial<Rule>): Rule => ({
  name: 'r',
  query: '',
  advanced: false,
  outcome: { variation: 'on' },
  ...over,
})
const half = { percentage: { on: 50, off: 50 } }

describe('hasSplit', () => {
  it('sees a split on the default rule or an active rule', () => {
    expect(hasSplit({ default: half, rules: [] })).toBe(true)
    expect(hasSplit({ default: { variation: 'off' }, rules: [rule({ outcome: half })] })).toBe(true)
  })

  it('ignores plain variations and disabled rules', () => {
    expect(hasSplit({ default: { variation: 'off' }, rules: [rule({})] })).toBe(false)
    expect(hasSplit({ default: { variation: 'off' }, rules: [rule({ outcome: half, disabled: true })] })).toBe(false)
  })
})

describe('rulesSplittingOneValue', () => {
  const pinned = rule({
    name: 'one',
    outcome: half,
    condition: { attribute: 'accountId', operator: 'eq', value: '42' },
  })

  it('flags a split rule that pins the bucketing attribute to one value', () => {
    expect(rulesSplittingOneValue({ rules: [pinned] }, 'accountId').map((r) => r.name)).toEqual(['one'])
  })

  it('flags a single-item in and a pin inside an and group', () => {
    const inOne = rule({ outcome: half, condition: { attribute: 'accountId', operator: 'in', value: '42' } })
    const anded = rule({
      outcome: half,
      condition: { op: 'and', children: [{ attribute: 'country', operator: 'eq', value: 'FR' }, pinned.condition!] },
    })
    expect(rulesSplittingOneValue({ rules: [inOne, anded] }, 'accountId')).toHaveLength(2)
  })

  it('leaves other attributes, lists, or-groups, negations and non-split rules alone', () => {
    const rules = [
      rule({ outcome: half, condition: { attribute: 'country', operator: 'eq', value: 'FR' } }),
      rule({ outcome: half, condition: { attribute: 'accountId', operator: 'in', value: '1,2' } }),
      rule({
        outcome: half,
        condition: { op: 'or', children: [pinned.condition!, { attribute: 'country', operator: 'eq', value: 'FR' }] },
      }),
      rule({ outcome: half, condition: { ...pinned.condition!, not: true } }),
      rule({ condition: pinned.condition }),
    ]
    expect(rulesSplittingOneValue({ rules }, 'accountId')).toEqual([])
    expect(rulesSplittingOneValue({ rules: [pinned] }, '')).toEqual([])
  })
})

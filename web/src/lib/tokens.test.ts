import { describe, expect, it } from 'vitest'
import type { RuleGroupType } from 'react-querybuilder'
import type { Condition } from '@/lib/api'
import { tokensFromCondition, tokensFromGroup, tokensToText } from './tokens'

const describeCondition = (c?: Condition) => tokensToText(tokensFromCondition(c)) || 'anyone'

function group(combinator: 'and' | 'or', ...rules: RuleGroupType['rules']): RuleGroupType {
  return { combinator, rules }
}

function rule(field: string, operator: string, value = '') {
  return { field, operator, value }
}

const kinds = (tokens: ReturnType<typeof tokensFromGroup>) => tokens.map((t) => t.kind)

describe('tokensFromGroup', () => {
  it('splits a leaf into attribute, operator and value so each can be styled', () => {
    expect(kinds(tokensFromGroup(group('and', rule('tier', 'eq', 'gold'))))).toEqual([
      'attribute',
      'operator',
      'value',
    ])
  })

  it('emits one value token per item in a set', () => {
    const tokens = tokensFromGroup(group('and', rule('tier', 'in', 'gold, silver, bronze')))
    expect(tokens.filter((t) => t.kind === 'value').map((t) => 'text' in t && t.text)).toEqual([
      'gold',
      'silver',
      'bronze',
    ])
  })

  it('drops a leaf with no attribute or no value', () => {
    expect(tokensFromGroup(group('and', rule('', 'eq', 'gold')))).toEqual([])
    expect(tokensFromGroup(group('and', rule('tier', 'eq', '')))).toEqual([])
  })

  it('keeps a zero-arity operator with no value', () => {
    expect(kinds(tokensFromGroup(group('and', rule('tier', 'pr'))))).toEqual([
      'attribute',
      'operator',
    ])
  })

  it('parenthesises a nested group so precedence is visible', () => {
    const nested = group(
      'or',
      group('and', rule('tier', 'eq', 'gold'), rule('country', 'eq', 'US')),
      rule('account_id', 'eq', '42'),
    )
    expect(tokensToText(tokensFromGroup(nested))).toBe(
      '(tier equals gold and country equals US) or account_id equals 42',
    )
  })

  it('does not parenthesise the top level', () => {
    expect(tokensToText(tokensFromGroup(group('and', rule('tier', 'eq', 'gold'))))).toBe(
      'tier equals gold',
    )
  })
})

describe('tokensFromCondition', () => {
  it('keeps a negated group negated, the bug that inverted rule meaning', () => {
    const negated: Condition = {
      op: 'and',
      not: true,
      children: [{ attribute: 'tier', operator: 'eq', value: 'gold' }],
    }
    expect(kinds(tokensFromCondition(negated))[0]).toBe('negation')
    expect(describeCondition(negated)).toBe('not tier equals gold')
  })

  it('distinguishes a negated group from a plain one', () => {
    const plain: Condition = {
      op: 'and',
      children: [{ attribute: 'tier', operator: 'eq', value: 'gold' }],
    }
    const negated: Condition = { ...plain, not: true }
    expect(describeCondition(plain)).not.toBe(describeCondition(negated))
  })

  it('keeps mixed and/or nesting unambiguous, the precedence bug', () => {
    const mixed: Condition = {
      op: 'or',
      children: [
        {
          op: 'and',
          children: [
            { attribute: 'tier', operator: 'eq', value: 'gold' },
            { attribute: 'country', operator: 'eq', value: 'US' },
          ],
        },
        { attribute: 'account_id', operator: 'eq', value: '42' },
      ],
    }
    expect(describeCondition(mixed)).toBe(
      '(tier equals gold and country equals US) or account_id equals 42',
    )
  })

  it('falls back to anyone for an absent condition', () => {
    expect(describeCondition(undefined)).toBe('anyone')
  })
})

describe('tokensToText', () => {
  it('spells the final set join as or, which chips convey by position', () => {
    expect(tokensToText(tokensFromGroup(group('and', rule('tier', 'in', 'gold, silver'))))).toBe(
      'tier is one of gold or silver',
    )
  })

  it('uses commas between all but the last set item', () => {
    expect(
      tokensToText(tokensFromGroup(group('and', rule('tier', 'in', 'a, b, c')))),
    ).toBe('tier is one of a, b or c')
  })

  it('leaves no space inside parentheses', () => {
    const nested = group('or', group('and', rule('a', 'eq', '1'), rule('b', 'eq', '2')), rule('c', 'eq', '3'))
    const text = tokensToText(tokensFromGroup(nested))
    expect(text).not.toContain('( ')
    expect(text).not.toContain(' )')
  })
})

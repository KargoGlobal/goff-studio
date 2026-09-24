import { describe, expect, it } from 'vitest'
import type { RuleGroupType } from 'react-querybuilder'
import { VISIBLE_OPERATORS, describeGroup, groupFromCondition, queryFromGroup } from './query'

function group(combinator: 'and' | 'or', ...rules: RuleGroupType['rules']): RuleGroupType {
  return { combinator, rules }
}

function rule(field: string, operator: string, value = '') {
  return { field, operator, value }
}

function notGroup(combinator: 'and' | 'or', ...rules: RuleGroupType['rules']): RuleGroupType {
  return { combinator, not: true, rules }
}

describe('queryFromGroup', () => {
  it('emits the two-attribute OR case', () => {
    expect(queryFromGroup(group('or', rule('property_a', 'eq', 'x'), rule('property_b', 'eq', 'y'))))
      .toBe('(property_a eq "x") or (property_b eq "y")')
  })

  it('emits AND', () => {
    expect(queryFromGroup(group('and', rule('tier', 'eq', 'gold'), rule('country', 'eq', 'US'))))
      .toBe('(tier eq "gold") and (country eq "US")')
  })

  it('leaves numeric comparisons unquoted', () => {
    expect(queryFromGroup(group('and', rule('age', 'gt', '21')))).toBe('(age gt 21)')
  })

  it('keeps numeric-looking strings quoted for equality', () => {
    expect(queryFromGroup(group('and', rule('account_id', 'eq', '42')))).toBe('(account_id eq "42")')
  })

  it('emits bare booleans', () => {
    expect(queryFromGroup(group('and', rule('active', 'eq', 'true')))).toBe('(active eq true)')
  })

  it('omits the operand for zero-arity operators', () => {
    expect(queryFromGroup(group('and', rule('email', 'pr')))).toBe('(email pr)')
  })

  it('builds a list for is-one-of', () => {
    expect(queryFromGroup(group('and', rule('tier', 'in', 'gold, silver , bronze'))))
      .toBe('(tier in ["gold", "silver", "bronze"])')
  })

  it('escapes quotes in values', () => {
    expect(queryFromGroup(group('and', rule('name', 'eq', 'he said "hi"'))))
      .toBe('(name eq "he said \\"hi\\"")')
  })

  it('parenthesises nested groups', () => {
    const nested = group(
      'and',
      group('or', rule('tier', 'eq', 'gold'), rule('tier', 'eq', 'platinum')),
      group('or', rule('region', 'eq', 'us'), rule('region', 'eq', 'eu')),
    )
    expect(queryFromGroup(nested)).toBe(
      '((tier eq "gold") or (tier eq "platinum")) and ((region eq "us") or (region eq "eu"))',
    )
  })

  it('does not wrap a single condition in redundant parens', () => {
    expect(queryFromGroup(group('and', rule('a', 'eq', 'b')))).toBe('(a eq "b")')
  })

  it('skips conditions with a blank attribute', () => {
    expect(queryFromGroup(group('or', rule('', 'eq', 'x'), rule('b', 'eq', 'y')))).toBe('(b eq "y")')
  })

  it('skips conditions with no value', () => {
    expect(queryFromGroup(group('or', rule('a', 'eq', ''), rule('b', 'eq', 'y')))).toBe('(b eq "y")')
  })

  it('keeps zero as a real value rather than dropping it', () => {
    expect(queryFromGroup(group('and', rule('age', 'gt', '0')))).toBe('(age gt 0)')
  })

  it('keeps the string "false" as a real value', () => {
    expect(queryFromGroup(group('and', rule('active', 'eq', 'false')))).toBe('(active eq false)')
  })

  it('returns empty for an empty group', () => {
    expect(queryFromGroup(group('and'))).toBe('')
  })
})

describe('groupFromCondition', () => {
  it('rehydrates a flat OR from the backend', () => {
    const g = groupFromCondition({
      op: 'or',
      children: [
        { attribute: 'property_a', operator: 'eq', value: 'x' },
        { attribute: 'property_b', operator: 'eq', value: 'y' },
      ],
    })
    expect(g.combinator).toBe('or')
    expect(g.rules).toHaveLength(2)
    expect(queryFromGroup(g)).toBe('(property_a eq "x") or (property_b eq "y")')
  })

  it('rehydrates nested groups and round-trips them', () => {
    const original = {
      op: 'and' as const,
      children: [
        {
          op: 'or' as const,
          children: [
            { attribute: 'tier', operator: 'eq', value: 'gold' },
            { attribute: 'tier', operator: 'eq', value: 'platinum' },
          ],
        },
        { attribute: 'region', operator: 'eq', value: 'us' },
      ],
    }
    expect(queryFromGroup(groupFromCondition(original))).toBe(
      '((tier eq "gold") or (tier eq "platinum")) and (region eq "us")',
    )
  })

  it('wraps a bare leaf condition in a group', () => {
    const g = groupFromCondition({ attribute: 'tier', operator: 'eq', value: 'gold' })
    expect(g.rules).toHaveLength(1)
    expect(queryFromGroup(g)).toBe('(tier eq "gold")')
  })

  it('returns an empty group for no condition', () => {
    expect(groupFromCondition(undefined).rules).toHaveLength(0)
  })
})

describe('describeGroup', () => {
  it('reads as plain english', () => {
    expect(describeGroup(group('or', rule('plan', 'eq', 'pro'), rule('plan', 'eq', 'enterprise'))))
      .toBe('plan equals pro or plan equals enterprise')
  })

  it('renders lists readably', () => {
    expect(describeGroup(group('and', rule('plan', 'in', 'pro, enterprise, trial'))))
      .toBe('plan is one of pro, enterprise or trial')
  })

  it('never leaks query syntax', () => {
    const described = describeGroup(group('and', rule('tier', 'eq', 'gold')))
    expect(described).not.toContain('"')
    expect(described).not.toContain(' eq ')
  })
})

describe('is one of collapses to eq or in', () => {
  it('a single value compiles to eq, so the YAML stays idiomatic', () => {
    expect(queryFromGroup(group('and', rule('tier', 'in', 'gold')))).toBe('(tier eq "gold")')
  })

  it('several values compile to in', () => {
    expect(queryFromGroup(group('and', rule('tier', 'in', 'gold, silver')))).toBe(
      '(tier in ["gold", "silver"])',
    )
  })

  it('a single negated value compiles to ne', () => {
    expect(queryFromGroup(group('and', rule('tier', 'notin', 'gold')))).toBe('(tier ne "gold")')
  })

  it('several negated values compile to not in', () => {
    expect(queryFromGroup(group('and', rule('tier', 'notin', 'gold, silver')))).toBe(
      '(not (tier in ["gold", "silver"]))',
    )
  })

  it('the two-attribute OR case still works through the new operator', () => {
    expect(
      queryFromGroup(group('or', rule('property_a', 'in', 'x'), rule('property_b', 'in', 'y'))),
    ).toBe('(property_a eq "x") or (property_b eq "y")')
  })

  it('numeric-looking values stay quoted for equality', () => {
    expect(queryFromGroup(group('and', rule('account_id', 'in', '42')))).toBe(
      '(account_id eq "42")',
    )
  })

  it('a backend eq rehydrates into the is-one-of control', () => {
    const g = groupFromCondition({ attribute: 'tier', operator: 'eq', value: 'gold' })
    const first = g.rules[0] as { operator: string; value: string }
    expect(first.operator).toBe('in')
    expect(first.value).toBe('gold')
    expect(queryFromGroup(g)).toBe('(tier eq "gold")')
  })

  it('a backend ne rehydrates into is-not-one-of', () => {
    const g = groupFromCondition({ attribute: 'tier', operator: 'ne', value: 'gold' })
    const first = g.rules[0] as { operator: string }
    expect(first.operator).toBe('notin')
    expect(queryFromGroup(g)).toBe('(tier ne "gold")')
  })

  it('a backend in rehydrates and round-trips', () => {
    const g = groupFromCondition({ attribute: 'tier', operator: 'in', value: 'gold, silver' })
    expect(queryFromGroup(g)).toBe('(tier in ["gold", "silver"])')
  })

  it('equals is hidden from the operator dropdown', () => {
    const names = VISIBLE_OPERATORS.map((o) => o.name)
    expect(names).not.toContain('eq')
    expect(names).not.toContain('ne')
    expect(names).toContain('in')
    expect(names).toContain('notin')
  })

  it('single-value operators are unaffected', () => {
    expect(queryFromGroup(group('and', rule('name', 'co', 'admin')))).toBe('(name co "admin")')
    expect(queryFromGroup(group('and', rule('age', 'gt', '21')))).toBe('(age gt 21)')
    expect(queryFromGroup(group('and', rule('email', 'pr')))).toBe('(email pr)')
  })
})

describe('plain english stays readable through the is-one-of control', () => {
  it('a single value reads as equals, not "is one of x"', () => {
    expect(describeGroup(group('and', rule('tier', 'in', 'gold')))).toBe('tier equals gold')
  })

  it('several values read as is one of', () => {
    expect(describeGroup(group('and', rule('tier', 'in', 'gold, silver')))).toBe(
      'tier is one of gold or silver',
    )
  })

  it('a single negated value reads as does not equal', () => {
    expect(describeGroup(group('and', rule('tier', 'notin', 'gold')))).toBe(
      'tier does not equal gold',
    )
  })

  it('several negated values read as is not one of', () => {
    expect(describeGroup(group('and', rule('tier', 'notin', 'gold, silver')))).toBe(
      'tier is not one of gold or silver',
    )
  })
})

describe('inverted groups', () => {
  it('wraps a single negated condition', () => {
    expect(queryFromGroup(notGroup('and', rule('tier', 'eq', 'gold')))).toBe(
      '(not (tier eq "gold"))',
    )
  })

  it('parenthesises a negated multi-condition group, since not binds tighter', () => {
    expect(queryFromGroup(notGroup('or', rule('a', 'eq', '1'), rule('b', 'eq', '2')))).toBe(
      '(not ((a eq "1") or (b eq "2")))',
    )
  })

  it('negates a nested group without negating its siblings', () => {
    const q = queryFromGroup(
      group('and', rule('country', 'eq', 'US'), notGroup('and', rule('tier', 'eq', 'gold'))),
    )
    expect(q).toBe('(country eq "US") and (not (tier eq "gold"))')
  })

  it('round-trips a negated condition from the server', () => {
    const built = groupFromCondition({
      op: 'and',
      not: true,
      children: [{ attribute: 'tier', operator: 'eq', value: 'gold' }],
    })
    expect(built.not).toBe(true)
    expect(queryFromGroup(built)).toBe('(not (tier eq "gold"))')
  })

  it('describes an inversion in plain language', () => {
    expect(describeGroup(notGroup('and', rule('tier', 'eq', 'gold')))).toBe('not tier equals gold')
    expect(
      describeGroup(notGroup('or', rule('a', 'eq', '1'), rule('b', 'eq', '2'))),
    ).toBe('not (a equals 1 or b equals 2)')
  })
})

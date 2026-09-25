import type { RuleGroupType } from 'react-querybuilder'
import type { Condition } from '@/lib/api'
import { arityOf, groupFromCondition, labelOf } from '@/lib/query'

export type Token =
  | { kind: 'attribute'; text: string }
  | { kind: 'operator'; text: string }
  | { kind: 'value'; text: string }
  | { kind: 'combinator'; text: string }
  | { kind: 'negation'; text: string }
  | { kind: 'open' }
  | { kind: 'close' }

function leafTokens(field: string, operator: string, rawValue: unknown): Token[] {
  const attribute = field.trim()
  if (!attribute) return []

  const out: Token[] = [{ kind: 'attribute', text: attribute }]
  const arity = arityOf(operator)

  if (arity === 0) {
    out.push({ kind: 'operator', text: labelOf(operator) })
    return out
  }

  const raw = rawValue === undefined || rawValue === null ? '' : String(rawValue)
  if (raw.trim() === '') return []

  if (arity === 2) {
    const items = raw
      .split(',')
      .map((v) => v.trim())
      .filter(Boolean)
    if (items.length === 0) return []

    const negated = operator === 'notin'
    if (items.length === 1) {
      out.push({ kind: 'operator', text: negated ? 'does not equal' : 'equals' })
      out.push({ kind: 'value', text: items[0] })
      return out
    }

    out.push({ kind: 'operator', text: negated ? 'is not one of' : 'is one of' })
    for (const item of items) out.push({ kind: 'value', text: item })
    return out
  }

  out.push({ kind: 'operator', text: labelOf(operator) })
  out.push({ kind: 'value', text: raw.trim() })
  return out
}

export function tokensFromGroup(group: RuleGroupType, top = true): Token[] {
  const parts: Token[][] = []

  for (const child of group.rules) {
    if (typeof child === 'string') continue

    if ('rules' in child) {
      const nested = tokensFromGroup(child, false)
      if (nested.length > 0) parts.push(nested)
      continue
    }

    const leaf = leafTokens(child.field ?? '', child.operator, child.value)
    if (leaf.length > 0) parts.push(leaf)
  }

  if (parts.length === 0) return []

  const combinator = group.combinator === 'or' ? 'or' : 'and'
  const joined: Token[] = []
  parts.forEach((part, i) => {
    if (i > 0) joined.push({ kind: 'combinator', text: combinator })
    joined.push(...part)
  })

  const needsParens = !top && parts.length > 1
  const out: Token[] = []
  if (group.not) out.push({ kind: 'negation', text: 'not' })
  if (needsParens || (group.not && parts.length > 1)) {
    out.push({ kind: 'open' }, ...joined, { kind: 'close' })
    return out
  }
  out.push(...joined)
  return out
}

export function tokensFromCondition(condition?: Condition): Token[] {
  if (!condition) return []
  return tokensFromGroup(groupFromCondition(condition))
}

// Adjacent values are one set, so text spells the final join that chips convey by position.
export function tokensToText(tokens: Token[]): string {
  let out = ''
  tokens.forEach((token, i) => {
    const text = token.kind === 'open' ? '(' : token.kind === 'close' ? ')' : token.text
    const prev = tokens[i - 1]

    if (token.kind === 'value' && prev?.kind === 'value') {
      out += tokens[i + 1]?.kind === 'value' ? `, ${text}` : ` or ${text}`
      return
    }

    const tight = i === 0 || token.kind === 'close' || prev?.kind === 'open'
    out += (tight ? '' : ' ') + text
  })
  return out
}

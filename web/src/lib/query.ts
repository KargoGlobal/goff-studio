import type { Field, RuleGroupType, RuleType } from 'react-querybuilder'
import type { Condition } from '@/lib/api'

export interface OperatorSpec {
  name: string
  label: string
  arity: 0 | 1 | 2
}

export const OPERATORS: OperatorSpec[] = [
  { name: 'in', label: 'is one of', arity: 2 },
  { name: 'notin', label: 'is not one of', arity: 2 },
  { name: 'co', label: 'contains', arity: 1 },
  { name: 'sw', label: 'starts with', arity: 1 },
  { name: 'ew', label: 'ends with', arity: 1 },
  { name: 'gt', label: 'is greater than', arity: 1 },
  { name: 'ge', label: 'is greater than or equal to', arity: 1 },
  { name: 'lt', label: 'is less than', arity: 1 },
  { name: 'le', label: 'is less than or equal to', arity: 1 },
  { name: 'pr', label: 'is present', arity: 0 },
  { name: 'eq', label: 'equals', arity: 1 },
  { name: 'ne', label: 'does not equal', arity: 1 },
]

export const VISIBLE_OPERATORS = OPERATORS.filter((o) => o.name !== 'eq' && o.name !== 'ne')

const BY_NAME = new Map(OPERATORS.map((o) => [o.name, o]))

export function arityOf(operator: string): 0 | 1 | 2 {
  return BY_NAME.get(operator)?.arity ?? 1
}

export function labelOf(operator: string): string {
  return BY_NAME.get(operator)?.label ?? operator
}

const NUMERIC = new Set(['gt', 'ge', 'lt', 'le'])

function quote(raw: string): string {
  return `"${raw.replace(/\\/g, '\\\\').replace(/"/g, '\\"')}"`
}

function scalar(raw: string, operator: string): string {
  const value = raw.trim()

  if (value.toLowerCase() === 'true' || value.toLowerCase() === 'false') {
    return value.toLowerCase()
  }
  if (NUMERIC.has(operator) && value !== '' && Number.isFinite(Number(value))) {
    return value
  }
  return quote(value)
}

function list(raw: string): string {
  const items = raw
    .split(',')
    .map((v) => v.trim())
    .filter((v) => v !== '')
    .map((v) => scalar(v, 'eq'))
  return `[${items.join(', ')}]`
}

export function ruleToQuery(rule: RuleType): string {
  const attribute = (rule.field ?? '').trim()
  if (!attribute) return ''

  const arity = arityOf(rule.operator)
  if (arity === 0) return `(${attribute} ${rule.operator})`

  const raw = rule.value === undefined || rule.value === null ? '' : String(rule.value)
  if (raw.trim() === '') return ''

  if (arity === 2) {
    const items = raw
      .split(',')
      .map((v) => v.trim())
      .filter((v) => v !== '')

    const negated = rule.operator === 'notin'
    if (items.length === 1) {
      const op = negated ? 'ne' : 'eq'
      return `(${attribute} ${op} ${scalar(items[0], op)})`
    }
    const listed = `(${attribute} in ${list(raw)})`
    return negated ? `(not ${listed})` : listed
  }

  return `(${attribute} ${rule.operator} ${scalar(raw, rule.operator)})`
}

export function queryFromGroup(group: RuleGroupType): string {
  return renderGroup(group, true)
}

function renderGroup(group: RuleGroupType, top: boolean): string {
  const parts: string[] = []

  for (const child of group.rules) {
    if (typeof child === 'string') continue

    const rendered = 'rules' in child ? renderGroup(child, false) : ruleToQuery(child)
    if (rendered) parts.push(rendered)
  }

  if (parts.length === 0) return ''
  if (parts.length === 1) return group.not ? `(not ${parts[0]})` : parts[0]

  const joined = parts.join(` ${group.combinator ?? 'and'} `)
  if (group.not) return `(not (${joined}))`
  return top ? joined : `(${joined})`
}

export function groupFromCondition(condition?: Condition): RuleGroupType {
  if (!condition) return { combinator: 'and', rules: [] }

  if (condition.op) {
    return {
      combinator: condition.op,
      not: condition.not ?? false,
      rules: (condition.children ?? []).map(childToRule),
    }
  }

  return { combinator: 'and', rules: [childToRule(condition)] }
}

function childToRule(condition: Condition): RuleType | RuleGroupType {
  if (condition.op) {
    return {
      combinator: condition.op,
      not: condition.not ?? false,
      rules: (condition.children ?? []).map(childToRule),
    }
  }

  const operator = condition.operator ?? 'eq'
  if (operator === 'eq' || operator === 'ne') {
    return {
      field: condition.attribute ?? '',
      operator: operator === 'ne' ? 'notin' : 'in',
      value: condition.value ?? '',
    }
  }

  return {
    field: condition.attribute ?? '',
    operator,
    value: condition.value ?? '',
  }
}

export function fieldsFrom(attributes: string[]): Field[] {
  return attributes.map((name) => ({ name, label: name }))
}

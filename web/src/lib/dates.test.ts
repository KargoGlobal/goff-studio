import { describe, expect, it } from 'vitest'
import { formatWhen, toISO, toLocalInput } from './dates'

describe('toLocalInput', () => {
  it('is empty for an empty or unparseable value', () => {
    expect(toLocalInput('')).toBe('')
    expect(toLocalInput('tomorrow')).toBe('')
  })

  it('renders a datetime-local value without seconds', () => {
    expect(toLocalInput('2026-10-01T00:00:00Z')).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/)
  })
})

describe('toISO', () => {
  it('is empty for an empty or unparseable value', () => {
    expect(toISO('')).toBe('')
    expect(toISO('not a date')).toBe('')
  })

  it('emits RFC3339 with no milliseconds, which is what the API validates', () => {
    expect(toISO('2026-10-01T09:30')).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/)
  })

  it('round-trips a datetime-local value back through the input format', () => {
    const local = '2026-10-01T09:30'
    expect(toLocalInput(toISO(local))).toBe(local)
  })
})

describe('formatWhen', () => {
  it('passes an unparseable value through rather than showing Invalid Date', () => {
    expect(formatWhen('whenever')).toBe('whenever')
  })
})

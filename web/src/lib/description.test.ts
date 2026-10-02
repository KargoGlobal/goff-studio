import { describe, expect, it } from 'vitest'
import { descriptionOf } from './description'

describe('descriptionOf', () => {
  it('reads a trimmed string from metadata', () => {
    expect(descriptionOf({ metadata: { description: '  Gates checkout. ' } })).toBe('Gates checkout.')
  })

  it('ignores missing or non-string values', () => {
    expect(descriptionOf({})).toBe('')
    expect(descriptionOf({ metadata: { description: 42 } })).toBe('')
    expect(descriptionOf({ metadata: { description: ['a'] } })).toBe('')
  })
})

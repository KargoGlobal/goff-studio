import { describe, expect, it } from 'vitest'
import { INLINE_LIMIT, prettyJson, summarizeJson } from './jsonValue'

describe('summarizeJson', () => {
  it('keeps short values inline', () => {
    expect(summarizeJson(true)).toEqual({ inline: 'true', expandable: false })
    expect(summarizeJson('CC0001')).toEqual({ inline: '"CC0001"', expandable: false })
    expect(summarizeJson({ a: 1 })).toEqual({ inline: '{"a":1}', expandable: false })
  })

  it('makes long values expandable and sizes objects and arrays', () => {
    const exclude = ['IFAType', 'MaxAdDuration', 'CountryCode', 'RegionCode', 'IABCategories', 'ContentTitle']
    expect(summarizeJson({ exclude })).toMatchObject({ expandable: true, size: '1 key' })
    expect(summarizeJson(exclude)).toMatchObject({ expandable: true, size: '6 items' })
    expect(summarizeJson('x'.repeat(INLINE_LIMIT + 1))).toMatchObject({ expandable: true, size: undefined })
  })

  it('handles undefined without throwing', () => {
    expect(summarizeJson(undefined)).toEqual({ inline: 'undefined', expandable: false })
  })
})

describe('prettyJson', () => {
  it('indents nested values', () => {
    expect(prettyJson({ a: [1] })).toBe('{\n  "a": [\n    1\n  ]\n}')
  })
})

import { describe, expect, it } from 'vitest'
import { describeExperimentation } from './ExperimentationEditor'

describe('describeExperimentation', () => {
  it('describes a closed window in terms of on and off, not evaluation', () => {
    const got = describeExperimentation({
      start: '2026-10-01T00:00:00Z',
      end: '2026-11-01T00:00:00Z',
    })
    expect(got).toContain('On only between')
  })

  it('says a start-only window is off until it opens', () => {
    expect(describeExperimentation({ start: '2026-10-01T00:00:00Z' })).toMatch(/^Off until/)
  })

  it('says an end-only window turns off afterwards', () => {
    expect(describeExperimentation({ end: '2026-11-01T00:00:00Z' })).toMatch(/then off$/)
  })

  it('does not claim a window when neither bound is set', () => {
    expect(describeExperimentation({})).toBe('No window set')
  })
})

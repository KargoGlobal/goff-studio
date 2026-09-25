import { describe, expect, it } from 'vitest'
import { describeEffectiveState, effectiveState } from './schedule'

const isWindowClosed = (w?: Parameters<typeof effectiveState>[0]['experimentation'], now?: Date) =>
  effectiveState({ enabled: true, experimentation: w }, now) !== 'on'

const now = new Date('2026-06-15T12:00:00Z')

describe('isWindowClosed', () => {
  it('is open when there is no window at all', () => {
    expect(isWindowClosed(undefined, now)).toBe(false)
  })

  it('is open inside a closed window', () => {
    expect(
      isWindowClosed({ start: '2026-01-01T00:00:00Z', end: '2026-12-01T00:00:00Z' }, now),
    ).toBe(false)
  })

  it('is closed before the start', () => {
    expect(isWindowClosed({ start: '2026-09-01T00:00:00Z' }, now)).toBe(true)
  })

  it('is closed after the end', () => {
    expect(isWindowClosed({ end: '2026-02-01T00:00:00Z' }, now)).toBe(true)
  })

  it('ignores an unparseable bound rather than closing the flag', () => {
    expect(isWindowClosed({ start: 'whenever' }, now)).toBe(false)
  })
})

describe('effectiveState', () => {
  it('is off whenever disable is set, window or not', () => {
    expect(effectiveState({ enabled: false }, now)).toBe('off')
    expect(
      effectiveState({ enabled: false, experimentation: { start: '2026-01-01T00:00:00Z' } }, now),
    ).toBe('off')
  })

  it('is on when enabled with no window', () => {
    expect(effectiveState({ enabled: true }, now)).toBe('on')
  })

  it('is on when enabled inside the window', () => {
    expect(
      effectiveState(
        { enabled: true, experimentation: { start: '2026-01-01T00:00:00Z', end: '2026-12-01T00:00:00Z' } },
        now,
      ),
    ).toBe('on')
  })

  it('is scheduled when enabled but the window has not opened', () => {
    expect(
      effectiveState({ enabled: true, experimentation: { start: '2026-09-01T00:00:00Z' } }, now),
    ).toBe('scheduled')
  })

  it('is expired when enabled but the window has closed', () => {
    expect(
      effectiveState({ enabled: true, experimentation: { end: '2026-02-01T00:00:00Z' } }, now),
    ).toBe('expired')
  })

  it('reports expired, not scheduled, once a fully past window is over', () => {
    expect(
      effectiveState(
        { enabled: true, experimentation: { start: '2026-01-01T00:00:00Z', end: '2026-02-01T00:00:00Z' } },
        now,
      ),
    ).toBe('expired')
  })
})

describe('describeEffectiveState', () => {
  it('never claims a scheduled or expired flag is on', () => {
    for (const state of ['scheduled', 'expired', 'off'] as const) {
      expect(describeEffectiveState(state, { end: '2026-02-01T00:00:00Z' })).toMatch(/^Off/)
    }
  })

  it('describes a plain on flag', () => {
    expect(describeEffectiveState('on')).toBe('On')
  })
})

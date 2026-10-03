import { describe, expect, it } from 'vitest'
import { defaultTeam, teamChoices } from './teams'

describe('teamChoices', () => {
  it('labels the empty name as No team and keeps server order', () => {
    expect(
      teamChoices([
        { name: 'payments', file: 'production/payments.goff.yaml' },
        { name: '', file: 'production/flags.goff.yaml' },
      ]),
    ).toEqual([
      { value: 'payments', label: 'payments' },
      { value: '', label: 'No team' },
    ])
  })

  it('offers nothing when the server sends nothing', () => {
    expect(teamChoices(null)).toEqual([])
  })
})

describe('defaultTeam', () => {
  it('picks the first offered team, which may be no team', () => {
    expect(defaultTeam([{ name: '', file: 'production/flags.goff.yaml' }])).toBe('')
    expect(defaultTeam([])).toBeUndefined()
  })
})

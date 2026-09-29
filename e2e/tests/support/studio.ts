import { expect, test as base, type Locator, type Page } from '@playwright/test'
import { dumpURL } from '../../scripts/ports.mjs'

export const PAYMENTS = 'production/payments.goff.yaml'
export const GROWTH = 'production/growth.goff.yaml'
export const USER_EMAIL = 'jaime@acme.com'
export const USER_NAME = 'Jaime Moncayo'
export const ENV = 'production'

export interface Commit {
  path: string
  message: string
  author: string
  email: string
  when: string
}

export interface RepoDump {
  files: Record<string, string>
  shas: Record<string, string>
  commits: Commit[]
  user: { name: string; email: string; subject: string }
}

export async function dump(): Promise<RepoDump> {
  const res = await fetch(`${dumpURL}/`)
  if (!res.ok) throw new Error(`dump endpoint returned ${res.status}`)
  return (await res.json()) as RepoDump
}

export async function resetRepo(): Promise<void> {
  const res = await fetch(`${dumpURL}/reset`, { method: 'POST' })
  if (!res.ok) throw new Error(`reset returned ${res.status}`)
}

// BUG-1 in e2e/README.md: the env segment is doubled because Shell's nested Routes use absolute paths.
export const listPath = (env = ENV) => `/env/${env}`
export const detailPath = (key: string, env = ENV) => `/env/${env}/flags/${key}`

export async function signIn(page: Page): Promise<void> {
  await page.goto('/')
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page.getByText(USER_EMAIL)).toBeVisible()
}

export async function gotoFlagList(page: Page, env = ENV): Promise<void> {
  await page.goto(listPath(env))
  await expect(page.getByRole('heading', { name: 'Feature flags' })).toBeVisible()
}

export async function gotoFlagDetail(page: Page, key: string, env = ENV): Promise<void> {
  await page.goto(detailPath(key, env))
  await expect(page.getByRole('heading', { name: key, exact: true })).toBeVisible()
}

export function reviewDialog(page: Page): Locator {
  return page.getByRole('dialog', { name: 'Review this change' })
}

export async function waitForReviewReady(page: Page): Promise<Locator> {
  const dialog = reviewDialog(page)
  await expect(dialog).toBeVisible()
  await expect(dialog.getByText('Working out what will change…')).toBeHidden()
  return dialog
}

export async function confirmReview(page: Page, env = ENV): Promise<void> {
  const dialog = await waitForReviewReady(page)
  const save = dialog.getByRole('button', { name: 'Save change' })

  await expect(save).toBeDisabled()
  await dialog.getByLabel(`Type ${env} to confirm`).fill(env)
  await expect(save).toBeEnabled()

  await save.click()
  await expect(dialog).toBeHidden()
}

export async function expectSuccessToast(page: Page): Promise<void> {
  await expect(page.getByRole('status')).toContainText(
    'Saved. Live in apps within about 30 seconds.',
  )
}

export async function waitForCommits(count: number): Promise<Commit[]> {
  let commits: Commit[] = []
  await expect
    .poll(
      async () => {
        commits = (await dump()).commits
        return commits.length
      },
      { message: `waiting for ${count} commit(s)`, timeout: 15_000 },
    )
    .toBe(count)
  return commits
}

export function expectUserTrailer(message: string): void {
  expect(message).toContain(`GOFF-Studio-User: ${USER_EMAIL}`)
}

export const test = base.extend<{ freshRepo: void }>({
  freshRepo: [
    async ({}, use) => {
      await resetRepo()
      await use()
    },
    { auto: true },
  ],
})

export { expect }

export function chipGroup(scope: import('@playwright/test').Locator | import('@playwright/test').Page, index = 0) {
  return scope.locator('[role=group][aria-label*=Value]').nth(index)
}

export async function setChip(
  scope: import('@playwright/test').Locator | import('@playwright/test').Page,
  value: string,
  index = 0,
) {
  const group = chipGroup(scope, index)
  for (const remove of await group.getByRole('button').all()) {
    await remove.click()
  }
  await group.locator('input').fill(value)
  await group.locator('input').press('Enter')
}

export async function chipValues(
  scope: import('@playwright/test').Locator | import('@playwright/test').Page,
  index = 0,
) {
  return chipGroup(scope, index).locator('span').allTextContents()
}

// The flag list renders each flag as a clickable row rather than a key link.
export function flagRow(page: Page, key: string): Locator {
  return page.locator('tbody tr').filter({ has: page.getByText(key, { exact: true }) })
}

// Picks a value in one of the custom single-selects (a combobox button plus a listbox).
export async function chooseOption(scope: Page | Locator, label: string, option: string): Promise<void> {
  const page = 'page' in scope ? scope.page() : scope
  await scope.getByRole('combobox', { name: label, exact: true }).click()
  await page.getByRole('listbox').getByRole('option', { name: option, exact: true }).click()
  await expect(page.getByRole('listbox')).toBeHidden()
}

const MONTHS = [
  'January', 'February', 'March', 'April', 'May', 'June',
  'July', 'August', 'September', 'October', 'November', 'December',
]

// Sets a DateTimeInput through its calendar popover. `when` is local time, `YYYY-MM-DDTHH:MM`.
export async function pickDateTime(page: Page, label: string, when: string): Promise<void> {
  const [datePart, timePart] = when.split('T')
  const [year, month, day] = datePart.split('-').map(Number)
  const [hours, minutes] = timePart.split(':')

  const trigger = page.getByRole('button', { name: label, exact: true })
  await trigger.click()
  const picker = page.getByRole('dialog', { name: `${label} picker` })
  await expect(picker).toBeVisible()

  const shown = (await picker.getByRole('grid').getAttribute('aria-label')) ?? ''
  const [shownMonth, shownYear] = shown.split(' ')
  const steps = (year - Number(shownYear)) * 12 + (month - 1 - MONTHS.indexOf(shownMonth))
  const nav = picker.getByRole('button', {
    name: steps > 0 ? 'Go to the Next Month' : 'Go to the Previous Month',
  })
  for (let i = 0; i < Math.abs(steps); i++) await nav.click()

  const monthName = MONTHS[month - 1]
  await expect(picker.getByRole('grid', { name: `${monthName} ${year}` })).toBeVisible()
  await picker
    .getByRole('button', { name: new RegExp(`${monthName} ${day}(st|nd|rd|th), ${year}`) })
    .click()

  await picker.getByLabel('Hours').fill(hours)
  await picker.getByLabel('Minutes').fill(minutes)
  await picker.getByLabel('Minutes').blur()
  await page.keyboard.press('Escape')
  await expect(picker).toBeHidden()

  await expect(trigger).toHaveText(`${datePart} · ${hours}:${minutes}`)
}

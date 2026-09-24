import { expect, detailPath, dump, setChip, signIn, test } from './support/studio'

async function review(page: import('@playwright/test').Page) {
  const dialog = page.getByRole('dialog')
  await expect(dialog).toBeVisible()
  await dialog.getByLabel('Type production to confirm').fill('production')
  await dialog.getByRole('button', { name: 'Save change' }).click()
  await expect(dialog).toBeHidden()
  await expect(page.getByRole('status').first()).toBeVisible()
}

test('a rule can be added to a flag that has none', async ({ page }) => {
  await signIn(page)
  await page.goto(detailPath('banner-test'))

  await page.getByRole('button', { name: 'Add rule' }).click()
  const panel = page.locator('.border-brand')
  await panel.getByLabel('New rule name').fill('pro-users')
  await panel.getByLabel('Attribute').fill('plan')
  await setChip(panel, 'pro')

  await page.getByRole('button', { name: 'Review the new rule' }).click()
  await review(page)

  const state = await dump()
  const growth = state.files['production/growth.goff.yaml']
  expect(growth).toContain('pro-users')
  expect(growth).toContain('(plan eq "pro")')
  expect(state.commits.at(-1)!.message).toContain('added rule pro-users')
})

test('a rule can be deleted', async ({ page }) => {
  await signIn(page)
  await page.goto(detailPath('new-checkout'))

  await page.getByRole('button', { name: 'Delete rule gold-cohort' }).click()
  await review(page)

  const state = await dump()
  expect(state.files['production/payments.goff.yaml']).not.toContain('gold-cohort')
  expect(state.commits.at(-1)!.message).toContain('deleted rule gold-cohort')
})

test('a rule can be disabled without deleting it', async ({ page }) => {
  await signIn(page)
  await page.goto(detailPath('new-checkout'))

  await page.getByRole('button', { name: 'Disable gold-cohort' }).click()
  await review(page)

  const state = await dump()
  const payments = state.files['production/payments.goff.yaml']
  expect(payments).toContain('gold-cohort')
  expect(payments).toContain('disable: true')
})

test('variations can be edited', async ({ page }) => {
  await signIn(page)
  await page.goto(detailPath('banner-test'))

  await page.getByRole('button', { name: 'Edit variations' }).click()
  await page.getByRole('button', { name: 'Add variation' }).click()
  await page.getByLabel('Variation 3 name').fill('maybe')

  await page.getByRole('button', { name: 'Review the variation changes' }).click()
  await review(page)

  const state = await dump()
  expect(state.files['production/growth.goff.yaml']).toContain('maybe')
})

test('a variation still used by a rule cannot be removed', async ({ page }) => {
  await signIn(page)
  await page.goto(detailPath('new-checkout'))

  await page.getByRole('button', { name: 'Edit variations' }).click()

  const remove = page.getByRole('button', { name: /Remove variation on/ })
  await expect(remove).toBeDisabled()

  const before = (await dump()).files['production/payments.goff.yaml']
  expect(before).toContain('gold-cohort')
})

test('rules can be reordered and keep their full content', async ({ page }) => {
  await signIn(page)
  await page.goto(detailPath('new-checkout'))

  await page.getByRole('button', { name: 'Add rule' }).click()
  const panel = page.locator('.border-brand')
  await panel.getByLabel('New rule name').fill('second')
  await panel.getByLabel('Attribute').fill('region')
  await setChip(panel, 'us')
  await page.getByRole('button', { name: 'Review the new rule' }).click()
  await review(page)

  await page.getByRole('button', { name: 'Move second earlier' }).click()
  await review(page)

  const state = await dump()
  const payments = state.files['production/payments.goff.yaml']
  expect(payments.indexOf('name: second')).toBeLessThan(payments.indexOf('name: gold-cohort'))
  expect(payments).toContain('(tier eq "gold") or (account_id eq "42")')
  expect(payments).toContain('percentage:')
})

test('every editing control on the detail page is reachable without a page error', async ({ page }) => {
  const errors: string[] = []
  page.on('pageerror', (e) => errors.push(e.message))

  await signIn(page)

  for (const key of ['new-checkout', 'banner-test']) {
    await page.goto(detailPath(key))
    await expect(page.getByRole('heading', { name: key })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Add rule' })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Edit variations' })).toBeVisible()
    await expect(page.getByRole('button', { name: `Delete ${key}` })).toBeVisible()
  }

  expect(errors, `uncaught page errors: ${errors.join(' | ')}`).toHaveLength(0)
})

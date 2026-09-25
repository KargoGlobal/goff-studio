import { dump, expect, PAYMENTS, signIn, test } from './support/studio'

const STAGING = 'staging/payments.goff.yaml'

test.beforeEach(async ({ page }) => {
  await signIn(page)
})

test('the compare view shows which settings differ between environments', async ({ page }) => {
  await page.goto('/env/staging/flags/new-checkout/compare')

  await expect(page.getByRole('heading', { name: 'new-checkout' })).toBeVisible()
  await expect(page.getByLabel('Target environment')).toHaveValue('production')

  await expect(page.getByText('Targeting rules').first()).toBeVisible()
  await expect(page.getByText('Not defined in this environment.')).toBeHidden()
})

test('a flag missing from the target is shown as absent, not as off', async ({ page }) => {
  await page.goto('/env/production/flags/banner-test/compare')

  await expect(page.getByText('Not defined in this environment.')).toBeVisible()
})

test('promoting staging targeting to production commits only that change', async ({ page }) => {
  const before = (await dump()).files[PAYMENTS]
  expect(before).toContain('on: 20')

  await page.goto('/env/staging/flags/new-checkout/compare')

  for (const label of ['Variations', 'Default rule', 'Schedule', 'Metadata', 'On/off state']) {
    const box = page.getByRole('checkbox', { name: new RegExp(`^${label}`) })
    if (await box.isChecked()) await box.uncheck()
  }

  await page.getByRole('button', { name: 'Review promotion' }).click()

  const dialog = page.getByRole('dialog', { name: 'Promote new-checkout to Production' })
  await expect(dialog).toBeVisible()
  await expect(dialog.getByText('Working out what will change…')).toBeHidden()
  await expect(dialog).toContainText('Promote new-checkout from staging to production')

  await dialog.getByRole('button', { name: 'Show file changes' }).click()
  await expect(dialog.locator('pre')).toContainText('60')

  const save = dialog.getByRole('button', { name: 'Save change' })
  await expect(save).toBeDisabled()
  await dialog.getByLabel('Type production to confirm').fill('production')
  await save.click()
  await expect(dialog).toBeHidden()

  await expect(page).toHaveURL(/\/env\/production\/flags\/new-checkout$/)
  await expect(page.getByRole('heading', { name: 'new-checkout', exact: true })).toBeVisible()

  await expect
    .poll(async () => (await dump()).files[PAYMENTS])
    .toContain('"on": 60')

  const after = (await dump()).files[PAYMENTS]
  expect(after).toContain('version: "3.1.0"')
  expect(after).not.toContain('disable: true')
  expect((await dump()).files[STAGING]).toContain('on: 60')
})

test('the on/off state is not promoted unless it is ticked', async ({ page }) => {
  await page.goto('/env/staging/flags/new-checkout/compare')

  const state = page.getByRole('checkbox', { name: /^On\/off state/ })
  await expect(state).not.toBeChecked()
})

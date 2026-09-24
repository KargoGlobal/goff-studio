import { confirmReview, detailPath, dump, expect, expectSuccessToast, signIn, test } from './support/studio'

const PLATFORM = 'production/platform.goff.yaml'

test.beforeEach(async ({ page }) => {
  await signIn(page)
  await page.goto(detailPath('ramped'))
})

test('a progressive rollout is shown in plain language instead of being hidden', async ({
  page,
}) => {
  await expect(page.getByText(/0% off on .* ramping to 100% on by/)).toBeVisible()
  await expect(page.getByRole('button', { name: 'Edit ramp' })).toBeVisible()
})

test('a ramp can be edited and lands in the file', async ({ page }) => {
  await page.getByRole('button', { name: 'Edit ramp' }).click()

  await page.getByLabel('initial percentage').fill('10')
  await page.getByLabel('end percentage').fill('90')
  await page.getByLabel('initial date').fill('2027-01-01T09:00')
  await page.getByLabel('end date').fill('2027-03-01T09:00')

  await page.getByRole('button', { name: 'Review change' }).click()

  const dialog = page.getByRole('dialog', { name: 'Review this change' })
  await expect(dialog).toContainText('progressive rollout on ramp')
  await dialog.getByText('Show file changes').click()
  await expect(dialog).toContainText('percentage: 10')
  await confirmReview(page)
  await expectSuccessToast(page)

  const file = (await dump()).files[PLATFORM]
  expect(file).toContain('percentage: 10')
  expect(file).toContain('percentage: 90')
  expect(file).toContain('2027-')
  expect(file).toContain('query: (region eq "us")')
})

test('the end date must be after the start date', async ({ page }) => {
  await page.getByRole('button', { name: 'Edit ramp' }).click()

  await page.getByLabel('initial date').fill('2027-06-01T09:00')
  await page.getByLabel('end date').fill('2027-01-01T09:00')
  await page.getByRole('button', { name: 'Review change' }).click()

  await expect(page.getByRole('alert')).toContainText('end date must be after')
  await expect(page.getByRole('dialog')).toBeHidden()
})

test('a ramp can be removed, leaving the rule serving one variation', async ({ page }) => {
  await page.getByRole('button', { name: 'Remove ramp' }).click()

  const dialog = page.getByRole('dialog', { name: 'Review this change' })
  await expect(dialog).toContainText('Remove the progressive rollout on ramp')
  await confirmReview(page)
  await expectSuccessToast(page)

  const file = (await dump()).files[PLATFORM]
  expect(file).not.toContain('progressiveRollout')
  expect(file).toContain('query: (region eq "us")')
  expect(file).toContain('variation: "on"')
})

test('editing the rule query leaves the ramp untouched', async ({ page }) => {
  await page.getByRole('button', { name: 'Edit conditions for ramp' }).click()

  const group = page.locator('[role=group][aria-label*=Value]').first()
  for (const remove of await group.getByRole('button').all()) {
    await remove.click()
  }
  await group.locator('input').fill('eu')
  await group.locator('input').press('Enter')

  await page.getByRole('button', { name: 'Review conditions for ramp' }).click()
  await confirmReview(page)
  await expectSuccessToast(page)

  const file = (await dump()).files[PLATFORM]
  expect(file).toContain('progressiveRollout')
  expect(file).toContain('percentage: 100')
  expect(file).toContain('"eu"')
})

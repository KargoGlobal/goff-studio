import { dump, expect, gotoFlagList, GROWTH, signIn, test } from './support/studio'

test.beforeEach(async ({ page }) => {
  await signIn(page)
  await gotoFlagList(page)
})

test('a team comes from metadata, and a flag without one reads as unassigned', async ({ page }) => {
  const filter = page.getByLabel('Filter by team')

  await expect(filter.locator('option')).toHaveText(['All teams', 'payments', 'platform', 'No team'])

  const banner = page.getByRole('row', { name: /banner-test/ })
  await expect(banner).not.toContainText('growth')
  await expect(banner).toContainText('—')

  await expect(page.getByRole('row', { name: /new-checkout/ })).toContainText('payments')
})

test('the no-team filter selects exactly the flags with no metadata team', async ({ page }) => {
  const filter = page.getByLabel('Filter by team')

  await filter.selectOption({ label: 'No team' })
  await expect(page.getByRole('link', { name: 'banner-test', exact: true })).toBeVisible()
  await expect(page.getByRole('link', { name: 'new-checkout', exact: true })).toBeHidden()

  await filter.selectOption({ label: 'payments' })
  await expect(page.getByRole('link', { name: 'new-checkout', exact: true })).toBeVisible()
  await expect(page.getByRole('link', { name: 'banner-test', exact: true })).toBeHidden()
})

test('creating a flag asks for a team, not a file, and records it in metadata', async ({ page }) => {
  await page.getByRole('link', { name: 'Create flag' }).click()

  await expect(page.getByLabel('File')).toBeHidden()

  await page.getByLabel('Flag key').fill('growth-promo')
  await page.getByLabel('Team', { exact: true }).selectOption('growth')
  await page.getByRole('button', { name: 'Create flag' }).click()

  await expect(page.getByRole('heading', { name: 'growth-promo', exact: true })).toBeVisible()

  const stored = (await dump()).files[GROWTH]
  expect(stored).toContain('growth-promo:')
  expect(stored).toContain('team: growth')

  await gotoFlagList(page)
  await expect(page.getByRole('row', { name: /growth-promo/ })).toContainText('growth')
})

test('a new team creates its own file and becomes selectable', async ({ page }) => {
  await page.getByRole('link', { name: 'Create flag' }).click()

  await page.getByRole('button', { name: 'New team' }).click()
  await page.getByLabel('New team name').fill('billing')
  await page.getByRole('button', { name: 'Add', exact: true }).click()

  await expect(page.getByRole('status')).toContainText('Created billing')
  await expect(page.getByLabel('Team', { exact: true })).toHaveValue('billing')

  const after = await dump()
  expect(after.files['production/billing.goff.yaml']).toContain('# Feature flags owned by billing')

  await page.getByLabel('Flag key').fill('invoice-v2')
  await page.getByRole('button', { name: 'Create flag' }).click()
  await expect(page.getByRole('heading', { name: 'invoice-v2', exact: true })).toBeVisible()

  const stored = (await dump()).files['production/billing.goff.yaml']
  expect(stored).toContain('invoice-v2:')
  expect(stored).toContain('team: billing')
})

test('a duplicate team name is refused without touching the repo', async ({ page }) => {
  await page.getByRole('link', { name: 'Create flag' }).click()

  const before = await dump()

  await page.getByRole('button', { name: 'New team' }).click()
  await page.getByLabel('New team name').fill('growth')
  await page.getByRole('button', { name: 'Add', exact: true }).click()

  await expect(page.getByRole('alert')).toContainText('already exists')
  expect((await dump()).files).toEqual(before.files)
})

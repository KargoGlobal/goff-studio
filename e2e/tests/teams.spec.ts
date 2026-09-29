import { chooseOption, dump, expect, flagRow, gotoFlagList, GROWTH, signIn, test } from './support/studio'

test.beforeEach(async ({ page }) => {
  await signIn(page)
  await gotoFlagList(page)
})

test('a team comes from metadata, and a flag without one reads as unassigned', async ({ page }) => {
  const banner = flagRow(page, 'banner-test')
  await expect(banner).not.toContainText('growth')
  await expect(banner).toContainText('—')

  await expect(flagRow(page, 'new-checkout')).toContainText('payments')

  // The team filter became a search that also matches teams. It must match the
  // metadata team, not the file a flag happens to live in.
  const search = page.getByLabel('Search flags')
  await search.fill('growth')
  await expect(page.getByText('No flags match')).toBeVisible()
  await expect(flagRow(page, 'banner-test')).toBeHidden()

  await search.fill('payments')
  await expect(flagRow(page, 'new-checkout')).toBeVisible()
  await expect(flagRow(page, 'banner-test')).toBeHidden()
})

// Suspected regression: the redesign replaced the team filter with a text search,
// so there is no longer any way to list only the flags that have no metadata team.
test.fixme('the no-team filter selects exactly the flags with no metadata team', async ({ page }) => {
  const filter = page.getByLabel('Filter by team')

  await filter.selectOption({ label: 'No team' })
  await expect(flagRow(page, 'banner-test')).toBeVisible()
  await expect(flagRow(page, 'new-checkout')).toBeHidden()

  await filter.selectOption({ label: 'payments' })
  await expect(flagRow(page, 'new-checkout')).toBeVisible()
  await expect(flagRow(page, 'banner-test')).toBeHidden()
})

test('creating a flag asks for a team, not a file, and records it in metadata', async ({ page }) => {
  await page.getByRole('link', { name: 'Create flag' }).click()

  await expect(page.getByLabel('File')).toBeHidden()

  await page.getByLabel('Flag key').fill('growth-promo')
  await chooseOption(page, 'Team', 'growth')
  await page.getByRole('button', { name: 'Create flag' }).click()

  await expect(page.getByRole('heading', { name: 'growth-promo', exact: true })).toBeVisible()

  const stored = (await dump()).files[GROWTH]
  expect(stored).toContain('growth-promo:')
  expect(stored).toContain('team: growth')

  await gotoFlagList(page)
  await expect(flagRow(page, 'growth-promo')).toContainText('growth')
})

test('a new team creates its own file and becomes selectable', async ({ page }) => {
  await page.getByRole('link', { name: 'Create flag' }).click()

  await page.getByRole('button', { name: 'New team' }).click()
  const dialog = page.getByRole('dialog', { name: 'New team' })
  await dialog.getByLabel('Name').fill('billing')
  await dialog.getByRole('button', { name: 'Save' }).click()

  await expect(page.getByRole('status')).toContainText('Created billing')
  await expect(dialog).toBeHidden()
  await expect(page.getByRole('combobox', { name: 'Team', exact: true })).toHaveText('billing')

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
  const dialog = page.getByRole('dialog', { name: 'New team' })
  await dialog.getByLabel('Name').fill('growth')
  await dialog.getByRole('button', { name: 'Save' }).click()

  await expect(dialog.getByRole('alert')).toContainText('already exists')
  expect((await dump()).files).toEqual(before.files)
})

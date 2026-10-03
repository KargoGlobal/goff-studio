import { chooseOption, dump, expect, flagRow, gotoFlagList, GROWTH, signIn, test } from './support/studio'

test.beforeEach(async ({ page }) => {
  await signIn(page)
  await gotoFlagList(page)
})

test('a team comes from its file, and the search matches it', async ({ page }) => {
  await expect(flagRow(page, 'banner-test')).toContainText('growth')
  await expect(flagRow(page, 'new-checkout')).toContainText('payments')

  const search = page.getByLabel('Search flags')
  await search.fill('growth')
  await expect(flagRow(page, 'banner-test')).toBeVisible()
  await expect(flagRow(page, 'new-checkout')).toBeHidden()
})

test('a file for an undeclared team is marked unknown', async ({ page }) => {
  await expect(flagRow(page, 'ramped')).toContainText('platform (unknown team)')
})

test('the team list is the declared teams plus No team, with no way to add one', async ({ page }) => {
  await page.getByRole('link', { name: 'Create flag' }).click()

  await expect(page.getByRole('button', { name: 'New team' })).toBeHidden()
  await page.getByRole('combobox', { name: 'Team', exact: true }).click()
  const options = page.getByRole('listbox').getByRole('option')
  await expect(options).toHaveText(['growth', 'payments', 'billing', 'No team'])
})

test('creating a flag for a team records it in metadata', async ({ page }) => {
  await page.getByRole('link', { name: 'Create flag' }).click()

  await page.getByLabel('Flag key').fill('growth-promo')
  await chooseOption(page, 'Team', 'growth')
  await page.getByRole('button', { name: 'Create flag' }).click()

  await expect(page.getByRole('heading', { name: 'growth-promo', exact: true })).toBeVisible()

  const stored = (await dump()).files[GROWTH]
  expect(stored).toContain('growth-promo:')
  expect(stored).toContain('team: growth')
})

test('the first flag for a declared team creates its file', async ({ page }) => {
  await page.getByRole('link', { name: 'Create flag' }).click()

  await page.getByLabel('Flag key').fill('invoice-v2')
  await chooseOption(page, 'Team', 'billing')
  await expect(page.getByText('production/billing.goff.yaml')).toBeVisible()
  await page.getByRole('button', { name: 'Create flag' }).click()
  await expect(page.getByRole('heading', { name: 'invoice-v2', exact: true })).toBeVisible()

  const stored = (await dump()).files['production/billing.goff.yaml']
  expect(stored).toContain('# Feature flags owned by billing')
  expect(stored).toContain('invoice-v2:')
  expect(stored).toContain('team: billing')
})

test('a flag with no team goes to the environment file', async ({ page }) => {
  await page.getByRole('link', { name: 'Create flag' }).click()

  await page.getByLabel('Flag key').fill('shared-kill-switch')
  await chooseOption(page, 'Team', 'No team')
  await page.getByRole('button', { name: 'Create flag' }).click()
  await expect(page.getByRole('heading', { name: 'shared-kill-switch', exact: true })).toBeVisible()

  const stored = (await dump()).files['production/flags.goff.yaml']
  expect(stored).toContain('shared-kill-switch:')
  expect(stored).not.toContain('team:')
})

test('the sidebar opens the Teams page, and a count links to that team in the flag list', async ({ page }) => {
  await page.getByRole('navigation').getByRole('link', { name: 'Teams' }).click()

  await expect(page.getByRole('heading', { name: 'Teams' })).toBeVisible()
  const rows = page.getByRole('row')
  await expect(rows).toHaveCount(4)
  await expect(page.getByRole('row', { name: 'payments' })).toContainText('you edit')
  await expect(page.getByRole('link', { name: '1 payments flags in staging' })).toBeVisible()
  await expect(page.getByRole('link', { name: '0 billing flags in production' })).toBeVisible()

  await page.getByRole('link', { name: '1 growth flags in production' }).click()
  await expect(page.getByLabel('Search flags')).toHaveValue('growth')
  await expect(flagRow(page, 'banner-test')).toBeVisible()
  await expect(flagRow(page, 'new-checkout')).toBeHidden()
})

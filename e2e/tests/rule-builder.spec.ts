import {
  chipValues,
  setChip,
  confirmReview,
  dump,
  expect,
  expectSuccessToast,
  expectUserTrailer,
  GROWTH,
  gotoFlagDetail,
  PAYMENTS,
  signIn,
  test,
  waitForCommits,
  waitForReviewReady,
} from './support/studio'

function whoMatches(page: import('@playwright/test').Page) {
  return page
    .locator('div')
    .filter({ has: page.getByText('Who this matches', { exact: true }) })
    .last()
}

test.beforeEach(async ({ page }) => {
  await signIn(page)
  await gotoFlagDetail(page, 'new-checkout')
  await page.getByRole('button', { name: /^Edit conditions for/ }).click()
})

test('the rule builder loads the existing query as editable conditions', async ({ page }) => {
  await expect(page.getByLabel('Attribute').first()).toHaveValue('tier')
  await expect(page.getByLabel('Attribute').nth(1)).toHaveValue('account_id')

  expect(await chipValues(page, 0)).toEqual(['gold'])
  expect(await chipValues(page, 1)).toEqual(['42'])

  await expect(whoMatches(page)).toContainText(
    'If tier equals gold or account_id equals 42',
  )
})

test('editing a condition value commits the rebuilt query string', async ({ page }) => {
  await setChip(page, 'platinum', 0)

  await expect(whoMatches(page)).toContainText(
    'If tier equals platinum or account_id equals 42',
  )

  await page.getByRole('button', { name: /^Review conditions for/ }).click()

  const dialog = await waitForReviewReady(page)
  await expect(dialog).toContainText('Change who gold-cohort targets on new-checkout')

  await dialog.getByRole('button', { name: 'Show file changes' }).click()
  await expect(dialog.locator('pre')).toContainText('(tier eq "platinum") or (account_id eq "42")')

  await confirmReview(page)
  await expectSuccessToast(page)

  const [commit] = await waitForCommits(1)
  expect(commit.path).toBe(PAYMENTS)
  expect(commit.message).toContain('updated targeting for gold-cohort')
  expectUserTrailer(commit.message)

  const payments = (await dump()).files[PAYMENTS]
  expect(payments).toContain('query: (tier eq "platinum") or (account_id eq "42")')
  expect(payments).not.toContain('tier eq "gold"')
})

test('changing the attribute and operator commits the matching query', async ({ page }) => {
  await page.getByLabel('Attribute').first().fill('plan')
  await page.locator('select[title=Operator]').first().selectOption('sw')
  await page.getByLabel('Value').first().fill('enterprise')

  await expect(whoMatches(page)).toContainText('plan starts with enterprise')

  await page.getByRole('button', { name: /^Review conditions for/ }).click()
  await confirmReview(page)
  await expectSuccessToast(page)

  await waitForCommits(1)

  const payments = (await dump()).files[PAYMENTS]
  expect(payments).toContain('(plan sw "enterprise")')
  expect(payments).toContain('(account_id eq "42")')
})

test('removing a condition narrows the committed query', async ({ page }) => {
  await page.locator('button[title="Remove condition"]').last().click()

  await expect(page.getByLabel('Attribute')).toHaveCount(1)
  await expect(whoMatches(page)).toContainText('If tier equals gold')

  await page.getByRole('button', { name: /^Review conditions for/ }).click()
  await confirmReview(page)
  await expectSuccessToast(page)

  await waitForCommits(1)

  const payments = (await dump()).files[PAYMENTS]
  expect(payments).toContain('query: (tier eq "gold")')
  expect(payments).not.toContain('account_id')
})

test('cancelling the rule editor leaves the file untouched', async ({ page }) => {
  const before = await dump()

  await setChip(page, 'platinum', 0)
  await page.getByRole('button', { name: 'Cancel' }).click()

  await expect(page.locator('[role=group][aria-label*=Value]')).toHaveCount(0)

  const after = await dump()
  expect(after.commits).toEqual([])
  expect(after.files[PAYMENTS]).toBe(before.files[PAYMENTS])
  expect(after.files[GROWTH]).toBe(before.files[GROWTH])
})

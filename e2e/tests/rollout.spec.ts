import {
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

test.beforeEach(async ({ page }) => {
  await signIn(page)
  await gotoFlagDetail(page, 'new-checkout')
})

test('the sliders start at the percentages stored in the file', async ({ page }) => {
  await expect(page.getByLabel('on percentage')).toHaveValue('20')
  await expect(page.getByLabel('off percentage')).toHaveValue('80')
  await expect(page.getByText('Always adds up to 100%.')).toBeVisible()
})

test('moving a slider surfaces the new split and enables Apply', async ({ page }) => {
  await expect(page.getByRole('button', { name: /^Apply the split for/ })).toBeDisabled()

  await page.getByLabel('on percentage').fill('55')

  await expect(page.getByLabel('on percentage')).toHaveValue('55')
  await expect(page.getByLabel('off percentage')).toHaveValue('45')
  await expect(page.getByRole('button', { name: /^Apply the split for/ })).toBeEnabled()
})

test('saving new rollout percentages commits them to the rule', async ({ page }) => {
  await page.getByLabel('on percentage').fill('55')
  await expect(page.getByLabel('off percentage')).toHaveValue('45')

  await page.getByRole('button', { name: /^Apply the split for/ }).click()

  const dialog = await waitForReviewReady(page)
  await expect(dialog).toContainText('Change the split on gold-cohort on new-checkout')
  await expect(dialog).toContainText('on: 55%')
  await expect(dialog).toContainText('off: 45%')

  await confirmReview(page)
  await expectSuccessToast(page)

  const [commit] = await waitForCommits(1)
  expect(commit.path).toBe(PAYMENTS)
  expect(commit.message).toContain('gold-cohort rollout')
  expect(commit.message).toContain('55% on')
  expect(commit.message).toContain('45% off')
  expectUserTrailer(commit.message)

  const state = await dump()
  const payments = state.files[PAYMENTS]

  expect(payments).toMatch(/"on":\s*55/)
  expect(payments).toMatch(/"off":\s*45/)
  expect(payments).toContain('query: (tier eq "gold") or (account_id eq "42")')
})

test('a rollout commit does not disturb the growth file', async ({ page }) => {
  const before = (await dump()).files[GROWTH]

  await page.getByLabel('on percentage').fill('70')
  await page.getByRole('button', { name: /^Apply the split for/ }).click()
  await confirmReview(page)
  await expectSuccessToast(page)

  await waitForCommits(1)

  expect((await dump()).files[GROWTH]).toBe(before)
})

test('two variations cannot both be set to 100', async ({ page }) => {
  await page.getByLabel('on percentage').fill('100')
  await page.getByLabel('off percentage').fill('100')

  await expect(page.getByLabel('on percentage')).toHaveValue('0')
  await expect(page.getByLabel('off percentage')).toHaveValue('100')
})

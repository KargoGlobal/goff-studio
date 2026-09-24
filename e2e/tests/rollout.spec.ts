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
  await expect(page.getByText(/Weights, relative to each other/)).toBeVisible()
})

test('moving a slider surfaces the new split and the review button', async ({ page }) => {
  await expect(page.getByRole('button', { name: /^Review the split for/ })).toBeHidden()

  await page.getByLabel('on percentage').fill('55')

  await expect(page.getByLabel('on percentage')).toHaveValue('55')
  await expect(page.getByText(/Total 135/)).toBeVisible()
  await expect(page.getByRole('button', { name: /^Review the split for/ })).toBeVisible()
})

test('saving new rollout percentages commits them to the rule', async ({ page }) => {
  await page.getByLabel('on percentage').fill('55')
  await page.getByLabel('off percentage').fill('45')

  await expect(page.getByText(/Weights, relative to each other/)).toBeVisible()

  await page.getByRole('button', { name: /^Review the split for/ }).click()

  const dialog = await waitForReviewReady(page)
  await expect(dialog).toContainText('Change gold-cohort on new-checkout')
  await expect(dialog).toContainText('55% on')
  await expect(dialog).toContainText('45% off')

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
  await page.getByRole('button', { name: /^Review the split for/ }).click()
  await confirmReview(page)
  await expectSuccessToast(page)

  await waitForCommits(1)

  expect((await dump()).files[GROWTH]).toBe(before)
})

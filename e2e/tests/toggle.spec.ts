import {
  confirmReview,
  dump,
  expect,
  expectSuccessToast,
  expectUserTrailer,
  GROWTH,
  gotoFlagList,
  PAYMENTS,
  reviewDialog,
  signIn,
  test,
  waitForCommits,
  waitForReviewReady,
} from './support/studio'

test.beforeEach(async ({ page }) => {
  await signIn(page)
  await gotoFlagList(page)
})

test('toggling a protected flag opens a review dialog that gates on typing the env name', async ({
  page,
}) => {
  await page.getByRole('switch', { name: 'Turn new-checkout off' }).click()

  const dialog = await waitForReviewReady(page)

  await expect(dialog).toContainText('Turn new-checkout off for everyone')
  await expect(dialog).toContainText('This is a protected environment')

  const save = dialog.getByRole('button', { name: 'Save change' })
  await expect(save).toBeDisabled()

  const confirmInput = dialog.getByLabel('Type production to confirm')
  await confirmInput.fill('prod')
  await expect(save).toBeDisabled()

  await confirmInput.fill('production')
  await expect(save).toBeEnabled()

  await expect(dialog.getByRole('button', { name: 'Cancel' })).toBeEnabled()
  expect((await dump()).commits).toEqual([])
})

test('the review dialog can reveal the underlying file diff', async ({ page }) => {
  await page.getByRole('switch', { name: 'Turn new-checkout off' }).click()
  const dialog = await waitForReviewReady(page)

  await expect(dialog.locator('pre')).toBeHidden()

  await dialog.getByRole('button', { name: 'Show file changes' }).click()

  const diff = dialog.locator('pre')
  await expect(diff).toBeVisible()
  await expect(diff).toContainText('+  disable: true')

  await dialog.getByRole('button', { name: 'Hide file changes' }).click()
  await expect(diff).toBeHidden()
})

test('cancelling the review dialog commits nothing', async ({ page }) => {
  await page.getByRole('switch', { name: 'Turn new-checkout off' }).click()
  const dialog = await waitForReviewReady(page)

  await dialog.getByRole('button', { name: 'Cancel' }).click()
  await expect(dialog).toBeHidden()

  const state = await dump()
  expect(state.commits).toEqual([])
  expect(state.files[PAYMENTS]).not.toContain('disable: true')
})

test('confirming the toggle shows a success toast and commits disable: true', async ({ page }) => {
  await page.getByRole('switch', { name: 'Turn new-checkout off' }).click()
  await confirmReview(page)

  await expectSuccessToast(page)

  const commits = await waitForCommits(1)
  const [commit] = commits

  expect(commit.path).toBe(PAYMENTS)
  expect(commit.message).toContain('[production] payments/new-checkout: disabled')
  expectUserTrailer(commit.message)
  expect(commit.email).toBe('jaime@acme.com')

  const state = await dump()
  expect(state.files[PAYMENTS]).toContain('disable: true')
})

test('committing to payments leaves the untouched growth file byte-identical', async ({ page }) => {
  const before = (await dump()).files[GROWTH]

  await page.getByRole('switch', { name: 'Turn new-checkout off' }).click()
  await confirmReview(page)
  await expectSuccessToast(page)

  const commits = await waitForCommits(1)
  expect(commits[0].path).toBe(PAYMENTS)

  const after = await dump()
  expect(after.files[GROWTH]).toBe(before)
  expect(after.commits.map((c) => c.path)).not.toContain(GROWTH)
})

test('the review dialog is not shown again after a successful save', async ({ page }) => {
  await page.getByRole('switch', { name: 'Turn new-checkout off' }).click()
  await confirmReview(page)
  await expectSuccessToast(page)
  await waitForCommits(1)

  await expect(reviewDialog(page)).toBeHidden()
  await expect(page.getByRole('switch', { name: 'Turn new-checkout on' })).toBeVisible()
})

import {
  confirmReview,
  detailPath,
  dump,
  expect,
  expectSuccessToast,
  PAYMENTS,
  signIn,
  test,
  waitForCommits,
  waitForReviewReady,
} from './support/studio'

test.beforeEach(async ({ page }) => {
  await signIn(page)
})

test('split by is tucked away until set, then shows its badge and warnings', async ({ page }) => {
  await page.goto(detailPath('new-checkout'))

  const advanced = page.locator('details', { has: page.getByText('Advanced', { exact: true }) })
  await expect(advanced).not.toHaveAttribute('open', '')
  await expect(page.getByText(/^split by /)).toBeHidden()

  await advanced.getByText('Advanced', { exact: true }).click()
  await expect(advanced).toContainText('Each evaluation is split on its own')
  await advanced.getByRole('button', { name: 'Change' }).click()
  await page.getByLabel('Split by attribute').fill('account_id')
  await advanced.getByRole('button', { name: 'Review change' }).click()

  const dialog = await waitForReviewReady(page)
  await expect(dialog).toContainText('Split new-checkout by account_id')
  await confirmReview(page)
  await expectSuccessToast(page)

  const [commit] = await waitForCommits(1)
  expect(commit.message).toContain('new-checkout: split by account_id')
  const file = (await dump()).files[PAYMENTS]
  expect(file).toContain('bucketingKey: account_id')
  expect(file).toContain('version: "3.1.0"')

  await page.reload()
  await expect(advanced).toHaveAttribute('open', '')
  await expect(page.getByText('split by account_id', { exact: true })).toBeVisible()
  await expect(advanced).toContainText('Requests without account_id skip every rule')
})

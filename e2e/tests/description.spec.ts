import {
  confirmReview,
  detailPath,
  dump,
  expect,
  expectSuccessToast,
  flagRow,
  GROWTH,
  gotoFlagList,
  listPath,
  PAYMENTS,
  signIn,
  test,
  waitForCommits,
  waitForReviewReady,
} from './support/studio'

test.beforeEach(async ({ page }) => {
  await signIn(page)
})

test('the logo goes to the flag list instead of leaving Studio', async ({ page }) => {
  await page.goto(detailPath('new-checkout'))
  await page.getByRole('link', { name: 'Studio home: all flags' }).click()
  await expect(page).toHaveURL(new RegExp(`${listPath()}$`))
  await expect(page.getByRole('heading', { name: 'Feature flags' })).toBeVisible()
})

test('the list shows descriptions, not a summary of the rules', async ({ page }) => {
  await gotoFlagList(page)
  await expect(flagRow(page, 'new-checkout')).not.toContainText('tier equals gold')
  await expect(flagRow(page, 'new-checkout')).not.toContainText('Serves')
})

test('a description can be added, edited and removed through review', async ({ page }) => {
  await page.goto(detailPath('new-checkout'))

  await page.getByRole('button', { name: 'Add a description' }).click()
  await page.getByLabel('Flag description').fill('Gates the new checkout flow.')
  await page.getByRole('button', { name: 'Review', exact: true }).click()
  const dialog = await waitForReviewReady(page)
  await expect(dialog).toContainText('Update the description of new-checkout')
  await confirmReview(page)
  await expectSuccessToast(page)

  await expect(page.getByText('Gates the new checkout flow.')).toBeVisible()
  const [commit] = await waitForCommits(1)
  expect(commit.message).toContain('new-checkout: updated the description')
  const file = (await dump()).files[PAYMENTS]
  expect(file).toContain('description: Gates the new checkout flow.')
  expect(file).toContain('# Payment flags, owned by @acme/payments')

  await gotoFlagList(page)
  await expect(flagRow(page, 'new-checkout')).toContainText('Gates the new checkout flow.')
  await page.getByPlaceholder(/Search flags/).fill('checkout flow')
  await expect(flagRow(page, 'new-checkout')).toBeVisible()
  await expect(flagRow(page, 'banner-test')).toBeHidden()

  await page.goto(detailPath('new-checkout'))
  await page.getByRole('button', { name: 'Edit the description' }).click()
  await page.getByLabel('Flag description').fill('')
  await page.getByRole('button', { name: 'Review', exact: true }).click()
  await expect(await waitForReviewReady(page)).toContainText('Remove the description of new-checkout')
  await confirmReview(page)
  await expectSuccessToast(page)

  await expect(page.getByRole('button', { name: 'Add a description' })).toBeVisible()
  await waitForCommits(2)
  expect((await dump()).files[PAYMENTS]).not.toContain('description:')
})

test('a new flag can be created with a description', async ({ page }) => {
  await page.goto(`${listPath()}/flags/new`)
  await page.getByLabel('Flag key').fill('dark-mode')
  await page.getByLabel(/^Description/).fill('Dark theme for the dashboard.')
  await page.getByRole('button', { name: /Create flag/ }).click()

  await expect(page.getByRole('heading', { name: 'dark-mode' })).toBeVisible()
  await expect(page.getByText('Dark theme for the dashboard.')).toBeVisible()
  await waitForCommits(1)
  const files = (await dump()).files
  expect(files[GROWTH] + files[PAYMENTS]).toContain('description: Dark theme for the dashboard.')
})

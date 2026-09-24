import {
  confirmReview,
  detailPath,
  dump,
  expect,
  expectSuccessToast,
  expectUserTrailer,
  PAYMENTS,
  signIn,
  test,
  waitForCommits,
} from './support/studio'

test.beforeEach(async ({ page }) => {
  await signIn(page)
})

test('renaming a flag keeps its rules, comments and unmodelled fields', async ({ page }) => {
  await page.goto(detailPath('new-checkout'))

  await page.getByRole('button', { name: 'Rename' }).click()
  const field = page.getByLabel('New flag key')
  await field.fill('checkout-v2')
  await page.getByRole('button', { name: 'Review', exact: true }).click()

  const dialog = page.getByRole('dialog', { name: 'Review this change' })
  await expect(dialog).toContainText('Rename new-checkout to checkout-v2')
  await confirmReview(page)
  await expectSuccessToast(page)

  await expect(page.getByRole('heading', { name: 'checkout-v2' })).toBeVisible()
  expect(page.url()).toContain('checkout-v2')

  const after = await dump()
  const file = after.files[PAYMENTS]
  expect(file).toContain('checkout-v2:')
  expect(file).not.toContain('new-checkout:')
  expect(file).toContain('# Payment flags, owned by @acme/payments')
  expect(file).toContain('version: "3.1.0"')
  expect(file).toContain('gold-cohort')
  expect(file).toContain('team: payments')

  const commits = await waitForCommits(1)
  expect(commits[0].path).toBe(PAYMENTS)
  expectUserTrailer(commits[0].message)
})

test('a rename to an existing key is refused and changes nothing', async ({ page }) => {
  await page.goto(detailPath('banner-test'))
  const before = await dump()

  await page.getByRole('button', { name: 'Rename' }).click()
  await page.getByLabel('New flag key').fill('new-checkout')
  await page.getByRole('button', { name: 'Review', exact: true }).click()

  await expect(page.getByRole('status')).toContainText('already exists')
  await expect(page.getByRole('dialog')).toBeHidden()

  expect((await dump()).files).toEqual(before.files)
})

test('renaming can be abandoned without touching anything', async ({ page }) => {
  await page.goto(detailPath('banner-test'))
  const before = await dump()

  await page.getByRole('button', { name: 'Rename' }).click()
  await page.getByLabel('New flag key').fill('nope')
  await page.getByRole('button', { name: 'Cancel' }).click()

  await expect(page.getByRole('heading', { name: 'banner-test' })).toBeVisible()
  expect((await dump()).files).toEqual(before.files)
})

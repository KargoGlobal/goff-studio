import { expect, detailPath, signIn, test } from './support/studio'

test('a flag with no targeting rules renders instead of crashing', async ({ page }) => {
  const errors: string[] = []
  page.on('pageerror', (e) => errors.push(e.message))

  await signIn(page)
  await page.goto(detailPath('banner-test'))

  await expect(page.getByRole('heading', { name: 'banner-test' })).toBeVisible()
  await expect(page.getByText('No targeting rules yet.')).toBeVisible()
  await expect(page.getByText('Default rule', { exact: true })).toBeVisible()
  await expect(page.getByText(/Serves\s+on to everyone/)).toBeVisible()

  expect(errors, `uncaught page errors: ${errors.join(' | ')}`).toHaveLength(0)
})

test('a flag with no rules still shows its variations and can be previewed', async ({ page }) => {
  await signIn(page)
  await page.goto(detailPath('banner-test'))

  await expect(page.getByText('on', { exact: true }).first()).toBeVisible()

  await page.getByRole('button', { name: 'Evaluate' }).click()
  await expect(page.getByText(/Gets/)).toBeVisible()
})

test('every flag detail page loads without a page error', async ({ page }) => {
  const errors: string[] = []
  page.on('pageerror', (e) => errors.push(`${e.message}`))

  await signIn(page)

  for (const key of ['new-checkout', 'banner-test']) {
    await page.goto(detailPath(key))
    await expect(page.getByRole('heading', { name: key })).toBeVisible()
  }

  expect(errors, `uncaught page errors: ${errors.join(' | ')}`).toHaveLength(0)
})

import { expect, flagRow, listPath, test, USER_EMAIL, USER_NAME } from './support/studio'

test('an unauthenticated visitor sees the sign-in page', async ({ page }) => {
  await page.goto('/')

  await expect(page.getByRole('button', { name: 'Sign in' })).toBeVisible()
  await expect(page.getByRole('heading', { name: /GO Feature Flag/ })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Feature flags' })).toBeHidden()
})

test('signing in completes the OIDC round trip and shows the signed-in user', async ({ page }) => {
  await page.goto('/')
  await page.getByRole('button', { name: 'Sign in' }).click()

  await expect(page.getByText(USER_NAME)).toBeVisible()
  await expect(page.getByText(USER_EMAIL)).toBeVisible()

  await expect(page.getByRole('link', { name: 'Production' })).toBeVisible()
  await expect(page.getByText(/You are editing/)).toBeVisible()
})

test('the flag list shows every flag in the repo', async ({ page }) => {
  await page.goto('/')
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page.getByText(USER_EMAIL)).toBeVisible()

  await page.goto(listPath())

  await expect(page.getByRole('heading', { name: 'Feature flags' })).toBeVisible()
  await expect(page.getByText('3 of 3 items')).toBeVisible()

  // The list is read-only now: each row opens the flag, and the Serving column
  // (fifth column) shows whether it is on. Toggling moved to the detail page.
  await expect(flagRow(page, 'new-checkout')).toBeVisible()
  await expect(flagRow(page, 'banner-test')).toBeVisible()

  await expect(flagRow(page, 'new-checkout').locator('td').nth(4)).toHaveText('true')
  await expect(flagRow(page, 'banner-test').locator('td').nth(4)).toHaveText('true')
})

test(
  'the flag list renders at the URL the sidebar links to',
  async ({ page }) => {
    await page.goto('/')
    await page.getByRole('button', { name: 'Sign in' }).click()
    await expect(page.getByText(USER_EMAIL)).toBeVisible()

    await expect(page).toHaveURL(/\/env\/production$/)
    await expect(page.getByRole('heading', { name: 'Feature flags' })).toBeVisible()
  },
)

test('a flag row opens the detail page', async ({ page }) => {
  await page.goto('/')
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page.getByText(USER_EMAIL)).toBeVisible()

  await page.goto(listPath())
  await flagRow(page, 'new-checkout').click()

  await expect(page).toHaveURL(/\/env\/production\/flags\/new-checkout$/)
  await expect(page.getByRole('heading', { name: 'new-checkout', exact: true })).toBeVisible()
})

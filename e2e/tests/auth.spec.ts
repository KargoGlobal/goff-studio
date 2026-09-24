import { expect, listPath, test, USER_EMAIL, USER_NAME } from './support/studio'

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
  await expect(page.getByText('3 flags in Production')).toBeVisible()

  await expect(page.getByRole('link', { name: 'new-checkout', exact: true })).toBeVisible()
  await expect(page.getByRole('link', { name: 'banner-test', exact: true })).toBeVisible()

  await expect(page.getByRole('switch', { name: 'Turn new-checkout off' })).toBeVisible()
  await expect(page.getByRole('switch', { name: 'Turn banner-test off' })).toBeVisible()
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

test('a flag key link opens the detail page', async ({ page }) => {
  await page.goto('/')
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page.getByText(USER_EMAIL)).toBeVisible()

  await page.goto(listPath())
  await page.getByRole('link', { name: 'new-checkout', exact: true }).click()

  await expect(page.getByRole('heading', { name: 'new-checkout', exact: true })).toBeVisible()
})

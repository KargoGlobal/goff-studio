import { dump, expect, signIn, test } from './support/studio'

test.beforeEach(async ({ page }) => {
  await signIn(page)
})

test('environments are the storage folders, named as they are on disk', async ({ page }) => {
  const nav = page.getByRole('navigation')
  await expect(nav.getByRole('link', { name: 'production' })).toBeVisible()
  await expect(nav.getByRole('link', { name: 'staging' })).toBeVisible()
  await expect(nav.getByRole('link', { name: 'Production', exact: true })).toBeHidden()
})

test('a new environment shows up in the sidebar as soon as it is created', async ({ page }) => {
  await page.getByRole('button', { name: 'New environment' }).click()

  const dialog = page.getByRole('dialog', { name: 'New environment' })
  await expect(dialog).toContainText('protectedEnvironments')
  await dialog.getByLabel('Name').fill('qa')
  await dialog.getByRole('button', { name: 'Create' }).click()
  await expect(dialog).toBeHidden()

  expect(Object.keys((await dump()).files)).toContain('qa/flags.goff.yaml')
  await expect(page.getByRole('navigation').getByRole('link', { name: 'qa' })).toBeVisible()
})

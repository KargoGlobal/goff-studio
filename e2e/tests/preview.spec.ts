import { expect, gotoFlagDetail, signIn, test } from './support/studio'

test.beforeEach(async ({ page }) => {
  await signIn(page)
  await gotoFlagDetail(page, 'new-checkout')
})

function previewPanel(page: import('@playwright/test').Page) {
  return page.locator('div').filter({ has: page.getByRole('heading', { name: 'Preview' }) }).last()
}

test('the preview panel evaluates a user and shows the result', async ({ page }) => {
  const panel = previewPanel(page)

  await expect(panel.getByRole('button', { name: 'Evaluate' })).toBeVisible()
  await panel.getByRole('button', { name: 'Evaluate' }).click()

  await expect(panel.getByText(/^Gets/)).toBeVisible()
  await expect(panel).toContainText('variation')
  await expect(panel).toContainText(/TARGETING_MATCH_SPLIT|DEFAULT|SPLIT/)
})

test('a user outside the targeting rule falls through to the default variation', async ({ page }) => {
  const panel = previewPanel(page)

  await panel.getByRole('textbox').first().fill('nobody-special')
  await panel.locator('textarea').fill('{"tier": "bronze"}')
  await panel.getByRole('button', { name: 'Evaluate' }).click()

  await expect(panel.getByText(/^Gets/)).toBeVisible()
  await expect(panel).toContainText('false')
  await expect(panel).toContainText('DEFAULT')
})

test('invalid attribute JSON reports an error instead of evaluating', async ({ page }) => {
  const panel = previewPanel(page)

  await panel.locator('textarea').fill('{ not json')
  await panel.getByRole('button', { name: 'Evaluate' }).click()

  await expect(page.getByRole('status')).toContainText('Context must be valid JSON')
  await expect(panel.getByText(/^Gets/)).toBeHidden()
})

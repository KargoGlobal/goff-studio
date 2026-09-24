import {
  confirmReview,
  expect,
  expectSuccessToast,
  gotoFlagDetail,
  signIn,
  test,
  USER_NAME,
  waitForCommits,
} from './support/studio'

function historyCard(page: import('@playwright/test').Page) {
  return page.locator('div').filter({ has: page.getByRole('heading', { name: 'History' }) }).last()
}

test('the history card is empty before any change', async ({ page }) => {
  await signIn(page)
  await gotoFlagDetail(page, 'new-checkout')

  await expect(historyCard(page)).toContainText('No changes recorded yet.')
})

test('a saved change shows up in the history card', async ({ page }) => {
  await signIn(page)
  await gotoFlagDetail(page, 'new-checkout')

  await page.getByRole('switch', { name: 'Turn new-checkout off' }).click()
  await confirmReview(page)
  await expectSuccessToast(page)
  await waitForCommits(1)

  await page.reload()

  const card = historyCard(page)
  await expect(card).toContainText('[production] payments/new-checkout: disabled')
  await expect(card).toContainText(USER_NAME)
})

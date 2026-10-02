import type { Page } from '@playwright/test'
import {
  confirmReview,
  detailPath,
  dump,
  expect,
  expectSuccessToast,
  flagRow,
  gotoFlagDetail,
  gotoFlagList,
  PAYMENTS,
  signIn,
  test,
  USER_EMAIL,
  waitForCommits,
} from './support/studio'

test.use({ viewport: { width: 375, height: 812 }, hasTouch: true, isMobile: true })

async function expectNoHorizontalScroll(page: Page) {
  const overflow = await page.evaluate(() =>
    [document.documentElement, document.querySelector('main')]
      .filter((el): el is HTMLElement => el !== null)
      .map((el) => el.scrollWidth - el.clientWidth),
  )
  for (const px of overflow) expect(px).toBeLessThanOrEqual(0)
}

test.beforeEach(async ({ page }) => {
  await signIn(page)
})

test('the sidebar is a drawer behind a menu button on phones', async ({ page }) => {
  await gotoFlagList(page)

  const menu = page.getByRole('button', { name: 'Open navigation' })
  const nav = page.getByRole('complementary', { name: 'Navigation' })
  await expect(menu).toBeVisible()
  await expect(nav).not.toBeInViewport()
  await expect(nav).toHaveAttribute('inert', '')

  await menu.tap()
  await expect(nav).toBeInViewport()
  await expect(menu).toHaveAttribute('aria-expanded', 'true')
  await expect(nav).not.toHaveAttribute('inert', '')
  await expect(page.getByText(USER_EMAIL)).toBeVisible()

  await page.keyboard.press('Escape')
  await expect(nav).not.toBeInViewport()
  await expect(menu).toBeFocused()

  await menu.tap()
  await page.getByRole('link', { name: 'staging' }).tap()
  await expect(page).toHaveURL(/\/env\/staging$/)
  await expect(nav).not.toBeInViewport()
})

test('the drawer locks page scroll while open', async ({ page }) => {
  await gotoFlagList(page)
  await page.getByRole('button', { name: 'Open navigation' }).tap()
  await expect(page.locator('body')).toHaveCSS('overflow', 'hidden')
  await page.getByRole('button', { name: 'Close navigation' }).tap()
  await expect(page.locator('body')).not.toHaveCSS('overflow', 'hidden')
})

test('no page scrolls sideways on a phone', async ({ page }) => {
  for (const url of [
    '/env/production',
    detailPath('new-checkout'),
    detailPath('banner-test'),
    '/env/production/flags/new',
    '/env/staging/flags/new-checkout/compare',
  ]) {
    await page.goto(url)
    await page.waitForLoadState('networkidle')
    await expectNoHorizontalScroll(page)
  }
})

test('the flag list keeps the essential columns and rows open the flag', async ({ page }) => {
  await gotoFlagList(page)
  await expect(page.getByRole('button', { name: 'Created' })).toBeHidden()
  await expect(page.getByRole('button', { name: 'Updated' })).toBeHidden()
  await expect(page.getByRole('button', { name: 'Serving' })).toBeVisible()

  await flagRow(page, 'new-checkout').tap()
  await expect(page.getByRole('heading', { name: 'new-checkout', exact: true })).toBeVisible()
})

test('form fields are at least 16px so iOS does not zoom on focus', async ({ page }) => {
  await gotoFlagDetail(page, 'new-checkout')
  const sizes = await page
    .locator('input:not([type=checkbox]):not([type=radio]):not([type=range]), textarea')
    .evaluateAll((els) => els.map((el) => parseFloat(getComputedStyle(el).fontSize)))
  expect(sizes.length).toBeGreaterThan(0)
  for (const size of sizes) expect(size).toBeGreaterThanOrEqual(16)
})

test('primary controls meet the 44px touch target', async ({ page }) => {
  await gotoFlagDetail(page, 'new-checkout')
  for (const name of ['Open navigation', 'Rename this flag', 'Edit variations', 'Evaluate']) {
    const box = await page.getByRole('button', { name }).boundingBox()
    expect(box?.height, name).toBeGreaterThanOrEqual(44)
  }
})

test('toggling a flag on a phone goes through the review sheet and commits', async ({ page }) => {
  await gotoFlagDetail(page, 'new-checkout')
  await page.getByRole('switch', { name: 'Turn new-checkout off' }).tap()

  const dialog = page.getByRole('dialog', { name: 'Review this change' })
  await expect(dialog).toBeVisible()
  const box = await dialog.boundingBox()
  const viewport = page.viewportSize()!
  expect(box!.width).toBe(viewport.width)
  expect(Math.round(box!.y + box!.height)).toBe(viewport.height)

  await confirmReview(page)
  await expectSuccessToast(page)
  const [commit] = await waitForCommits(1)
  expect(commit.path).toBe(PAYMENTS)
  expect((await dump()).files[PAYMENTS]).toContain('disable: true')
})

test('the date picker opens as a sheet that fits the screen', async ({ page }) => {
  await gotoFlagDetail(page, 'new-checkout')
  await page.getByRole('button', { name: 'Add window' }).tap()
  await page.getByRole('button', { name: 'Experimentation start', exact: true }).tap()

  const picker = page.getByRole('dialog', { name: 'Experimentation start picker' })
  await expect(picker).toBeVisible()
  const box = (await picker.boundingBox())!
  expect(box.x).toBeGreaterThanOrEqual(0)
  expect(box.x + box.width).toBeLessThanOrEqual(page.viewportSize()!.width)
  await expectNoHorizontalScroll(page)

  await page.keyboard.press('Escape')
  await expect(picker).toBeHidden()
})

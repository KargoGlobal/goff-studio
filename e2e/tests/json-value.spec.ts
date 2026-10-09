import type { Page } from '@playwright/test'
import { expect, gotoFlagDetail, signIn, test } from './support/studio'

async function horizontalOverflow(page: Page): Promise<number[]> {
  return page.evaluate(() =>
    [document.documentElement, document.querySelector('main')]
      .filter((el): el is HTMLElement => el !== null)
      .map((el) => el.scrollWidth - el.clientWidth),
  )
}

test.beforeEach(async ({ page }) => {
  await signIn(page)
  await gotoFlagDetail(page, 'request-filter')
})

test('a long JSON variation is previewed on one line and expands without widening the page', async ({ page }) => {
  await expect(page.getByText(/^\{"exclude":\["IFAType"/)).toBeVisible()
  for (const px of await horizontalOverflow(page)) expect(px).toBeLessThanOrEqual(0)
  const toggle = page.getByRole('button', { name: 'Show the full value of strict' })
  await expect(toggle).toHaveText('1 key')

  await toggle.click()
  const pretty = page.locator('pre').filter({ hasText: '"ImpPodIDs"' })
  await expect(pretty).toBeVisible()
  await expect(pretty).toContainText('"exclude": [\n    "IFAType",')
  for (const px of await horizontalOverflow(page)) expect(px).toBeLessThanOrEqual(0)

  await page.getByRole('button', { name: 'Hide the full value of strict' }).click()
  await expect(pretty).toBeHidden()
})

test('short JSON values stay inline with no expand control', async ({ page }) => {
  await expect(page.getByText('{"exclude":[]}', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Show the full value of loose' })).toHaveCount(0)
})

test('the preview result uses the same collapsed view', async ({ page }) => {
  await page.getByRole('button', { name: 'Evaluate' }).click()
  const toggle = page.getByRole('button', { name: 'Show the full result' })
  await expect(toggle).toBeVisible()
  await toggle.click()
  await expect(page.locator('pre').filter({ hasText: '"ImpPodIDs"' })).toBeVisible()
  for (const px of await horizontalOverflow(page)) expect(px).toBeLessThanOrEqual(0)
})

test('a blocked clipboard reports that the value was not copied', async ({ page }) => {
  await page.evaluate(() => {
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText: () => Promise.reject(new Error('denied')) },
    })
  })
  await page.getByRole('button', { name: 'Show the full value of strict' }).click()
  await page.getByRole('button', { name: 'Copy the value of strict' }).click()
  await expect(page.getByText('Could not copy the value of strict')).toBeVisible()
})

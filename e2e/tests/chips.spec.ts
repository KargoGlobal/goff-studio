import { expect, detailPath, signIn, test } from './support/studio'

test('the operator list hides equals in favour of is one of', async ({ page }) => {
  await signIn(page)
  await page.goto(detailPath('new-checkout'))
  await page.getByRole('button', { name: /^Edit conditions for/ }).click()

  const options = await page.locator('select.rule-operators').first().locator('option').allTextContents()
  expect(options).toContain('is one of')
  expect(options).toContain('is not one of')
  expect(options).not.toContain('equals')
  expect(options).not.toContain('does not equal')
})

test('typing a comma separated list becomes individual chips', async ({ page }) => {
  await signIn(page)
  await page.goto(detailPath('banner-test'))

  await page.getByRole('button', { name: 'Add rule' }).click()
  const panel = page.locator('.border-brand')
  await panel.getByLabel('Attribute').fill('tier')

  const values = panel.getByRole('group', { name: 'Value' })
  await values.locator('input').fill('1,2,3,4')
  await values.locator('input').press('Enter')

  await expect(values.locator('span')).toHaveCount(4)
  await expect(panel.locator('code').last()).toContainText('(tier in ["1", "2", "3", "4"])')
})

test('a chip can be removed', async ({ page }) => {
  await signIn(page)
  await page.goto(detailPath('banner-test'))

  await page.getByRole('button', { name: 'Add rule' }).click()
  const panel = page.locator('.border-brand')
  await panel.getByLabel('Attribute').fill('tier')

  const values = panel.getByRole('group', { name: 'Value' })
  await values.locator('input').fill('gold,silver')
  await values.locator('input').press('Enter')
  await expect(values.locator('span')).toHaveCount(2)

  await values.getByRole('button', { name: 'Remove silver' }).click()
  await expect(values.locator('span')).toHaveCount(1)
  await expect(panel.locator('code').last()).toContainText('(tier eq "gold")')
})

test('an existing equals rule rehydrates as a chip under is one of', async ({ page }) => {
  await signIn(page)
  await page.goto(detailPath('new-checkout'))
  await page.getByRole('button', { name: /^Edit conditions for/ }).click()

  const chips = page.locator('[role=group][aria-label*=Value] span')
  await expect(chips.first()).toHaveText('gold')
  await expect(page.locator('select.rule-operators').first()).toHaveValue('in')
})

test('a single chip commits as eq, several commit as in', async ({ page }) => {
  await signIn(page)
  await page.goto(detailPath('new-checkout'))
  await page.getByRole('button', { name: /^Edit conditions for/ }).click()

  const first = page.locator('[role=group][aria-label*=Value]').first()
  await first.locator('input').fill('platinum')
  await first.locator('input').press('Enter')

  await expect(page.locator('code').last()).toContainText('(tier in ["gold", "platinum"])')
  await expect(page.locator('code').last()).toContainText('(account_id eq "42")')
})

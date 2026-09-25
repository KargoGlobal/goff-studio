import { readFile } from 'node:fs/promises'
import { dump, expect, signIn, test, waitForCommits, waitForReviewReady } from './support/studio'

const listRow = (page: import('@playwright/test').Page, name: string) =>
  page.getByRole('row').filter({ has: page.getByRole('link', { name }) })

test.beforeEach(async ({ page }) => {
  await signIn(page)
})

test('the sidebar opens the experiments list with registry fields', async ({ page }) => {
  await page.getByRole('link', { name: 'Experiments' }).click()
  await expect(page.getByRole('heading', { name: 'Experiments' })).toBeVisible()

  const row = listRow(page, 'New checkout for gold')
  await expect(row).toContainText('running')
  await expect(row).toContainText('payments')
  await expect(row).toContainText('new-checkout')
  await expect(row).toContainText('entity')
  await expect(row).toContainText('10d run · 18d left')
  await expect(listRow(page, 'Ramp latency check')).toBeVisible()

  await page.getByLabel('Filter by team').selectOption('platform')
  await expect(listRow(page, 'New checkout for gold')).toBeHidden()
  await expect(listRow(page, 'Ramp latency check')).toBeVisible()

  await page.getByLabel('Filter by team').selectOption('all')
  await page.getByLabel('Filter by status').selectOption('draft')
  await expect(page.getByText('No experiments match')).toBeVisible()
})

test('hovering a row prefetches results and fills in the summary', async ({ page }) => {
  await page.goto('/experiments')
  const row = listRow(page, 'New checkout for gold')
  await expect(row).toBeVisible()
  expect((await dump()).analysisCalls).toBe(0)

  await row.hover()
  await expect(row).toContainText('+4.73%')
  await expect(row).toContainText('Roll out')
  await expect(row.getByText('OK')).toBeVisible()

  await row.getByRole('link', { name: 'New checkout for gold' }).click()
  await expect(page.getByRole('heading', { name: /New checkout for gold/ })).toBeVisible()
  await expect(page.getByText('Recommendation')).toBeVisible()
  // The detail page reuses the prefetched results and the server cache.
  expect((await dump()).analysisCalls).toBe(1)
})

test('the results page shows decision, metrics, CUPED, segments and diagnostics', async ({ page }) => {
  await page.goto('/experiments/new-checkout-gold-cohort')

  await expect(page.getByText('The new checkout raises click rate')).toBeVisible()
  await expect(page.getByRole('link', { name: 'new-checkout' })).toHaveAttribute('href', '/env/production/flags/new-checkout')
  await expect(page.getByText(/Traffic matches the planned split \(p = /)).toBeVisible()
  await expect(page.getByText('on significantly improves Click rate')).toBeVisible()

  const primary = page.getByRole('region', { name: 'Primary metrics' }).first()
  await expect(primary).toContainText('Click rate')
  await expect(primary).toContainText('+4.73%')
  await expect(primary).toContainText('VR 36%')
  await expect(primary.getByRole('img', { name: /Click rate, on: lift \+4\.73%/ })).toBeVisible()
  // CUPED is the headline estimate; the unadjusted readout sits alongside.
  await expect(primary.getByText('CUPED', { exact: true })).toBeVisible()
  await expect(primary.getByRole('columnheader', { name: 'Unadjusted' })).toBeVisible()
  await expect(primary.getByRole('img', { name: /Click rate, on, unadjusted: lift \+4\.73%/ })).toBeVisible()

  const guardrails = page.getByRole('region', { name: 'Guardrails' })
  await expect(guardrails).toContainText('Net CPM')
  await expect(guardrails).toContainText('pass')
  await expect(guardrails.getByRole('row', { name: /Average bid CPM/ })).toContainText('no data')
  await expect(guardrails.getByRole('row', { name: /Average bid CPM/ })).toContainText('no usable data')

  const secondary = page.getByRole('region', { name: 'Secondary metrics' }).first()
  await expect(secondary.getByRole('row', { name: /PMP revenue/ })).toContainText('n/a')

  await expect(page.getByRole('tab', { name: 'device' })).toHaveAttribute('aria-selected', 'true')
  await expect(page.getByRole('tabpanel')).toContainText('desktop')
  await expect(page.getByRole('tabpanel')).toContainText('mobile')

  await expect(page.getByRole('img', { name: 'Cumulative lift in Click rate over time' })).toBeVisible()
  await expect(page.getByText('traffic balance')).toBeVisible()
  await expect(page.getByText('data as of')).toBeVisible()
  await expect(page.getByText('sample data')).toHaveCount(0)
})

test('a sample ratio mismatch blocks the page with a red banner', async ({ page }) => {
  await page.goto('/experiments/ramped-ramp')
  const banner = page.getByRole('alert')
  await expect(banner).toContainText('Sample ratio mismatch: do not act on these results')
})

test('the readout downloads as Markdown', async ({ page }) => {
  await page.goto('/experiments/new-checkout-gold-cohort')
  await expect(page.getByText('Recommendation')).toBeVisible()

  const [download] = await Promise.all([
    page.waitForEvent('download'),
    page.getByRole('button', { name: 'Download readout as Markdown' }).click(),
  ])
  expect(download.suggestedFilename()).toBe('new-checkout-gold-cohort-readout.md')
  const md = await readFile(await download.path(), 'utf8')
  expect(md).toContain('# Experiment readout: New checkout for gold')
  expect(md).toContain('**Hypothesis:** The new checkout raises click rate')
  expect(md).toContain('**Recommendation: Roll out on.**')
  expect(md).toContain('| Click rate | on |')
  expect(md).toContain('| Net CPM | on |')
  expect(md).toContain('CUPED-adjusted; the unadjusted readout is shown alongside')
  expect(md).toContain('| +4.73% (CUPED) |')
  expect(md).toContain('no data (no usable data)')
})

test('creating an experiment goes through review and commits the registry file', async ({ page }) => {
  await page.goto('/experiments')
  await page.getByRole('button', { name: 'New experiment' }).click()
  await expect(page.getByRole('heading', { name: 'New experiment' })).toBeVisible()

  await page.getByLabel('Key', { exact: true }).fill('request-timeout-exp-region-a')
  await page.getByLabel('Name', { exact: true }).fill('Checkout copy')
  await page.getByLabel('Hypothesis').fill('Clearer copy raises clicks')
  await page.getByLabel('Flag', { exact: true }).selectOption('request-timeout')

  await expect(page.getByLabel('Owner')).toHaveValue('platform')
  await expect(page.getByRole('group', { name: 'Allocations' }).getByLabel('exp-region-a')).toBeChecked()
  await page.getByLabel('Control', { exact: true }).selectOption('control')

  await page.getByRole('group', { name: 'Primary metrics', exact: true }).getByLabel('Click rate').check()
  await expect(page.getByText('Detectable lift over the planned 28 days')).toContainText('±1.23%')

  await page.getByRole('button', { name: 'Review and create' }).click()
  const dialog = await waitForReviewReady(page)
  await expect(dialog).toContainText('Create experiment request-timeout-exp-region-a on request-timeout in production')
  await dialog.getByLabel('Type production to confirm').fill('production')
  await dialog.getByRole('button', { name: 'Save change' }).click()

  await expect(page.getByRole('heading', { name: /Checkout copy/ })).toBeVisible()
  await expect(page.getByText('There are no results for this experiment yet')).toBeVisible()

  const [c] = await waitForCommits(1)
  expect(c.path).toBe('experiments/request-timeout-exp-region-a.yaml')
  expect(c.message).toContain('[experiments] request-timeout-exp-region-a: created')
  const file = (await dump()).files['experiments/request-timeout-exp-region-a.yaml']
  expect(file).toContain('flag: request-timeout')
  expect(file).toContain('control: control')
  expect(file).toContain('status: draft')
})

test('the form refuses an over-long window before review', async ({ page }) => {
  await page.goto('/experiments/new')
  await page.getByLabel('End date').fill('2099-01-01')
  await expect(page.getByText(/longer than 8 weeks/)).toBeVisible()
  await page.getByRole('button', { name: 'Review and create' }).click()
  await expect(page.getByRole('alert')).toContainText('Experiments run for at most 8 weeks')
  expect((await dump()).commits).toEqual([])
})

test('editing an experiment records a status change', async ({ page }) => {
  await page.goto('/experiments/new-checkout-gold-cohort')
  await page.getByRole('link', { name: 'Edit' }).click()
  await page.getByLabel('Status').selectOption('stopped')
  await page.getByRole('button', { name: 'Review changes' }).click()

  const dialog = await waitForReviewReady(page)
  await expect(dialog).toContainText('status running to stopped')
  await dialog.getByLabel('Type production to confirm').fill('production')
  await dialog.getByRole('button', { name: 'Save change' }).click()

  await waitForCommits(1)
  expect((await dump()).files['experiments/new-checkout-gold-cohort.yaml']).toContain('status: stopped')
})

test('the metric catalog lists and adds metrics', async ({ page }) => {
  await page.getByRole('link', { name: 'Metrics' }).click()
  await expect(page.getByRole('heading', { name: 'Metric catalog' })).toBeVisible()
  await expect(page.getByRole('link', { name: 'Average bid CPM' })).toBeVisible()
  await expect(page.getByText('dsp_bid_price / dsp_bid_count')).toBeVisible()

  await page.getByRole('button', { name: 'New metric' }).click()
  await page.getByLabel('Key').fill('bid_count')
  await page.getByLabel('Name').fill('Bids per request')
  await page.getByLabel('Numerator').fill('dsp_bid_count')
  await page.getByRole('button', { name: 'Review and add' }).click()

  const dialog = await waitForReviewReady(page)
  await expect(dialog).toContainText('Add metric bid_count in the catalog')
  await dialog.getByRole('button', { name: 'Save change' }).click()
  await expect(page.getByRole('link', { name: 'Bids per request' })).toBeVisible()
  expect((await dump()).files['metrics/bid_count.yaml']).toContain('numerator: dsp_bid_count')
})

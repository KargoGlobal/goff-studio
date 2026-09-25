import { createHash } from 'node:crypto'
import {
  confirmReview,
  dump,
  expect,
  expectSuccessToast,
  gotoFlagDetail,
  signIn,
  test,
  waitForCommits,
  waitForReviewReady,
} from './support/studio'

const PLATFORM = 'production/platform.goff.yaml'

function shardOf(salt: string, subject: string, total = 10000): number {
  const digest = createHash('md5').update(`${salt}-${subject}`).digest()
  return digest.readUInt32BE(0) % total
}

function exposedSubject(): string {
  for (let i = 0; i < 10000; i++) {
    if (shardOf('c1e0a7d25f', `req-${i}`) < 100) return `req-${i}`
  }
  throw new Error('no exposed subject')
}

test.beforeEach(async ({ page }) => {
  await signIn(page)
  await gotoFlagDetail(page, 'request-timeout')
})

test('a rule with an allocation shows its experiment', async ({ page }) => {
  const panel = page.getByRole('region', { name: 'Experiment on exp-region-a' })
  await expect(panel).toContainText('request-timeout-exp-region-a')
  await expect(panel).toContainText('request · targetingKey')
  await expect(panel.getByLabel('Exposure for exp-region-a')).toHaveValue('1')
  await expect(panel.getByRole('row', { name: /control/ })).toContainText('50%')
  await expect(panel.getByRole('row', { name: /fast/ })).toContainText('50%')
})

test('exposure cannot be lowered without re-randomizing', async ({ page }) => {
  const input = page.locator('#exposure-exp-region-a')
  await input.fill('0.5')
  await expect(input).toHaveValue('1')
  await expect(page.getByRole('button', { name: /^Review ramp/ })).toBeHidden()

  await page.getByLabel(/Re-randomize: new salts/).check()
  await input.fill('0.5')
  await expect(input).toHaveValue('0.5')
  await expect(page.getByRole('button', { name: 'Review re-randomization' })).toBeVisible()
})

test('ramping exposure commits only the exposure ranges', async ({ page }) => {
  await page.locator('#exposure-exp-region-a').fill('2')
  await page.getByRole('button', { name: 'Review ramp to 2%' }).click()

  const dialog = await waitForReviewReady(page)
  await expect(dialog).toContainText('Exposure 1% → 2% in rule exp-region-a; existing subjects keep their arm')

  await confirmReview(page)
  await expectSuccessToast(page)

  const [commit] = await waitForCommits(1)
  expect(commit.path).toBe(PLATFORM)
  expect(commit.message).toContain('exposure 1% → 2% in rule exp-region-a')

  const file = (await dump()).files[PLATFORM]
  expect(file.match(/ranges: \[\[0, 200\]\]/g)).toHaveLength(2)
  expect(file).not.toContain('[[0, 100]]')
  expect(file).toContain('{salt: "9b3f41e2aa", ranges: [[0, 5000]]}')
})

test('preview explains the experiment assignment', async ({ page }) => {
  await page.getByLabel('User ID').fill(exposedSubject())
  await page.getByLabel('Context (JSON)').fill('{"region": "region-a"}')
  await page.getByRole('button', { name: 'Evaluate' }).click()

  await expect(page.getByText('experiment request-timeout-exp-region-a · rule exp-region-a · logged')).toBeVisible()
  const panel = page.getByRole('region', { name: 'Experiment on exp-region-a' })
  await expect(panel).toContainText('In the experiment')
})

test('stopping an experiment writes its end time', async ({ page }) => {
  await page.getByRole('button', { name: 'Stop now' }).click()
  const dialog = await waitForReviewReady(page)
  await expect(dialog).toContainText('Experiment window on rule exp-region-a')

  await confirmReview(page)
  await expectSuccessToast(page)

  const file = (await dump()).files[PLATFORM]
  expect(file).toMatch(/endAt: \d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z/)
})

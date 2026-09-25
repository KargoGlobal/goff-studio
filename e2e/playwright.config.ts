import { defineConfig, devices } from '@playwright/test'
import { appURL, dumpURL, e2eDir, ports } from './scripts/ports.mjs'

export default defineConfig({
  testDir: './tests',
  outputDir: './.test-results',
  fullyParallel: false,
  workers: 1,
  retries: process.env.CI ? 1 : 0,
  forbidOnly: Boolean(process.env.CI),
  timeout: 60_000,
  expect: { timeout: 10_000 },
  reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : [['list']],

  use: {
    baseURL: appURL,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    actionTimeout: 15_000,
  },

  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],

  webServer: [
    {
      command: './.bin/fakes',
      cwd: e2eDir,
      url: `${dumpURL}/`,
      reuseExistingServer: false,
      stdout: 'pipe',
      stderr: 'pipe',
      timeout: 30_000,
      env: {
        E2E_IDP_PORT: ports.idp,
        E2E_GITHUB_PORT: ports.github,
        E2E_DUMP_PORT: ports.dump,
        E2E_ANALYSIS_PORT: ports.analysis,
      },
    },
    {
      command: 'node scripts/start-app.mjs',
      cwd: e2eDir,
      url: `${appURL}/healthz`,
      reuseExistingServer: false,
      stdout: 'pipe',
      stderr: 'pipe',
      timeout: 60_000,
    },
  ],
})

import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

export const e2eDir = resolve(dirname(fileURLToPath(import.meta.url)), '..')
export const repoRoot = resolve(e2eDir, '..')
export const binDir = resolve(e2eDir, '.bin')

export const ports = {
  app: process.env.E2E_APP_PORT ?? '9400',
  idp: process.env.E2E_IDP_PORT ?? '9401',
  github: process.env.E2E_GITHUB_PORT ?? '9402',
  dump: process.env.E2E_DUMP_PORT ?? '9403',
  analysis: process.env.E2E_ANALYSIS_PORT ?? '9404',
}

export const appURL = `http://127.0.0.1:${ports.app}`
export const dumpURL = `http://127.0.0.1:${ports.dump}`

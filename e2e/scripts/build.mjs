import { execFileSync } from 'node:child_process'
import { existsSync, mkdirSync, rmSync } from 'node:fs'
import { resolve } from 'node:path'
import { binDir, e2eDir, repoRoot } from './ports.mjs'

function run(command, args, cwd) {
  execFileSync(command, args, { cwd, stdio: 'inherit' })
}

const webDist = resolve(repoRoot, 'cmd/goff-studio/dist/index.html')
const rebuildWeb = process.env.E2E_SKIP_WEB_BUILD !== '1' || !existsSync(webDist)

if (rebuildWeb) {
  const webDir = resolve(repoRoot, 'web')
  if (!existsSync(resolve(webDir, 'node_modules'))) {
    console.log('> npm ci (web)')
    run('npm', ['ci'], webDir)
  }
  console.log('> npm run build (web)')
  run('npm', ['run', 'build'], webDir)
} else {
  console.log('> skipping the web build because E2E_SKIP_WEB_BUILD=1')
}

mkdirSync(binDir, { recursive: true })

rmSync(resolve(binDir, 'goff-studio'), { force: true })

console.log('> go build goff-studio')
run('go', ['build', '-o', resolve(binDir, 'goff-studio'), './cmd/goff-studio'], repoRoot)

console.log('> go build fakes')
run('go', ['build', '-o', resolve(binDir, 'fakes'), '.'], resolve(e2eDir, 'harness'))

console.log('> build ok')

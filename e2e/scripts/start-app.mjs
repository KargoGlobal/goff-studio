import { spawn } from 'node:child_process'
import { resolve } from 'node:path'
import { binDir, e2eDir, ports } from './ports.mjs'

const idpProbe = `http://127.0.0.1:${ports.idp}/.well-known/openid-configuration`

async function waitForIDP(timeoutMs = 30_000) {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    try {
      const res = await fetch(idpProbe)
      if (res.ok) return
    } catch {
      // not up yet
    }
    await new Promise((r) => setTimeout(r, 150))
  }
  throw new Error(`fake IDP never came up at ${idpProbe}`)
}

await waitForIDP()

const child = spawn(
  resolve(binDir, 'goff-studio'),
  ['-config', resolve(e2eDir, 'harness/studio.e2e.yaml')],
  {
    stdio: 'inherit',
    env: {
      ...process.env,
      GOFF_STUDIO_GITHUB_API_BASE: `http://127.0.0.1:${ports.github}`,
      GOFF_STUDIO_ADDR: `127.0.0.1:${ports.app}`,
      GOFF_STUDIO_BASE_URL: `http://127.0.0.1:${ports.app}`,
      GOFF_STUDIO_OIDC_ISSUER_URL: `http://127.0.0.1:${ports.idp}`,
    },
  },
)

const stop = () => child.kill('SIGTERM')
process.on('SIGTERM', stop)
process.on('SIGINT', stop)
child.on('exit', (code) => process.exit(code ?? 0))

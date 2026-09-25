# Agent instructions

Instructions for AI coding agents (Claude Code, Codex and similar) working in this
repository. Humans should read [CONTRIBUTING.md](CONTRIBUTING.md); everything there
applies to agents too, and this file only adds what an agent needs to work safely
and verify its own changes.

## Read first

- [CONTRIBUTING.md](CONTRIBUTING.md): licensing, package layout, "where things go",
  code conventions and the security model.
- [README.md](README.md): what Studio does, the config keys, and the "Verified against
  GO Feature Flag" findings. Do not re-derive those; they were measured.
- [e2e/README.md](e2e/README.md) before touching the UI or the HTTP API.

## Hard rules

- **Licences.** New dependencies must be MIT, Apache-2.0, BSD or ISC. Check before adding.
  Prefer the standard library; a dependency needs a reason beyond convenience.
- **Stay vendor-neutral.** This project is intended for donation to the go-feature-flag
  organisation. Keep code, comments, fixtures and docs generic OIDC and generic storage.
  Do not reference any company's internal systems, other feature-flag vendors, real org
  names, or internal hostnames.
- **Sign off every commit** (`git commit -s`).
- **Conventional-commit PR titles** (`feat: …`, `fix(goff): …`). CI enforces it.
- **Security model is not negotiable.** Every permission check is server-side and
  default-deny. Every Git write goes through `Service.Save` (or the same checked path),
  which validates with GO Feature Flag's own validator and handles stale SHAs with 409.
  Never weaken `fileSha` handling.
- **The minimal-diff guarantee is load-bearing.** Any change to `internal/goff`
  (`adapter.go`, `yamldoc.go`) must keep untouched text byte-for-byte, including comments,
  key order, quoting and anchors. Add a golden test for any new preservation behaviour.
- **Never talk to real GitHub, a real IdP or a real analysis service in tests.** Use the
  `httptest` fakes and the `e2e/harness` fakes.
- Do not commit `studio.yaml`, `*.pem`, tokens, build outputs (`e2e/.bin/`,
  `cmd/goff-studio/dist/assets`) or compiled binaries. Keep `cmd/goff-studio/dist/.gitkeep`.

## Verify before you say done

Run these and paste the summary lines in your report. Do not claim a check you did not run.

```sh
make test          # Go tests across all modules, frontend typecheck and vitest
make lint          # gofmt, go vet, golangci-lint (skipped locally if not installed), oxlint
cd e2e && npm install && npx playwright install chromium && npm test   # if you touched UI or HTTP API
```

- `golangci-lint` is often not installed locally; CI runs it. If you install it, use the
  same version as CI and run `golangci-lint run ./...`.
- The e2e suite binds ports 9400–9403. If they are taken, set the port variables listed in
  `e2e/scripts/ports.mjs` (`E2E_APP_PORT`, `E2E_IDP_PORT`, `E2E_GITHUB_PORT`,
  `E2E_DUMP_PORT`) rather than killing processes you did not start.
- `npm test` rebuilds the frontend unless `E2E_SKIP_WEB_BUILD=1` is set; only skip it when
  you know the bundle in `cmd/goff-studio/dist` is current.
- When you fix a bug, add the test that would have caught it, and check it fails without
  the fix.

## Keep the UI fast

- Load new pages with `React.lazy` and keep heavy libraries out of the entry chunk. Check
  the `npm run build` size output before and after you add a page or a dependency.
- No charting library: small inline SVG components are the pattern.
- Server state goes through `@tanstack/react-query` and the typed `api` object in
  `web/src/lib/api.ts`. No bare `fetch` in components.
- Anything that calls an external service is cached server-side; do not add per-render
  calls to external services.

## Working with other agents

- Parallel agents should each use their own git worktree and branch. Never use bare
  `git stash`; the stash is shared across worktrees.
- Do not push, open PRs, or change deployment config unless the person you are working for
  asked for it.

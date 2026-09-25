# End-to-end tests

Playwright suite that drives the real Go binary serving the real React bundle,
against fake OIDC and GitHub servers. No network access and no real repo.

## Running

```bash
cd e2e
npm install                    # once
npx playwright install chromium # once
npm test
```

`npm test` builds everything it needs and then runs the suite. It starts and
stops the fakes and the app itself, so nothing needs to be running first.

Other scripts:

| Command | What it does |
| --- | --- |
| `npm test` | Build + run the whole suite (the one you want) |
| `npm run test:only` | Run tests, skip the build |
| `npm run test:headed` | Run with a visible browser |
| `E2E_REBUILD_WEB=1 npm test` | Force a `web/` rebuild (otherwise reused if present) |
| `npx playwright test tests/toggle.spec.ts` | One file |
| `npx playwright test -g "rollout"` | Filter by name |

If a port is already taken the run fails fast with
`http://127.0.0.1:9403/ is already used`. Free them with:

```bash
for p in 9400 9401 9402 9403 9404; do pid=$(lsof -ti :$p); [ -n "$pid" ] && kill $pid; done
```

## How it fits together

`scripts/build.mjs` builds the web bundle into `cmd/goff-studio/dist`, then
builds two binaries into `e2e/.bin/`: the app (`goff-studio`) and the fakes.
Playwright's `webServer` then boots the fakes, waits for them, and boots the app
pointed at them via `GOFF_STUDIO_GITHUB_API_BASE`.

Tests hit the Go server directly. There is no Vite dev server involved — the Go
binary serves the built SPA, which is what production does.

| Port | Service | Env var |
| --- | --- | --- |
| 9400 | the app under test | `E2E_APP_PORT` |
| 9401 | fake OIDC IDP | `E2E_IDP_PORT` |
| 9402 | fake GitHub Contents API | `E2E_GITHUB_PORT` |
| 9403 | state dump / reset | `E2E_DUMP_PORT` |
| 9404 | fake analysis service | `E2E_ANALYSIS_PORT` |

### The fakes (`harness/fakes.go`, `harness/analysis.go`)

Single stdlib-only Go program, no module dependencies.

- **IDP** serves OIDC discovery and a JWKS, and `/authorize` immediately 302s
  back to the app's `redirect_uri` with a code, so signing in needs no UI
  interaction. `/token` mints a real RS256-signed `id_token` with
  `sub`/`name`/`email`/`groups`, where groups is `["flags-admins"]`.
- **GitHub** serves two flag files under `production/` from memory, honours the
  blob-SHA optimistic concurrency check (409 on mismatch), and records commits.
- **GitHub** also serves two experiments (`experiments/checkout-gold-cohort.yaml`
  on `new-checkout`, `experiments/ramp-latency-check.yaml` on `ramped`, with
  dates relative to today) and five catalog metrics under `metrics/`.
- **Analysis** answers `GET /v1/experiments/{key}/results` for those two
  experiments (the second one with a sample ratio mismatch), 404s any other key,
  requires the bearer token `e2e-analysis-token`, and answers
  `POST /v1/experiments/power` with a fixed estimate. The app is started with
  `GOFF_STUDIO_ANALYSIS_BASE_URL` pointing at it, so the suite exercises the real
  proxy and cache; sample mode is covered by the Go server tests.
- **Dump** returns `{files, shas, commits, analysisCalls, user}` so a test can assert what was
  actually committed. `POST /reset` restores the fixtures; every test gets a
  clean repo through the auto-use `freshRepo` fixture in `tests/support/studio.ts`.

The configured user is `Jaime Moncayo <jaime@acme.com>`, in `flags-admins`,
which `harness/studio.e2e.yaml` grants `["*"]`. `production` is the only
environment and it is **protected**, so every write goes through the review
dialog.

### Fixtures

`production/payments.goff.yaml` holds `new-checkout`: a `gold-cohort` rule
(`(tier eq "gold") or (account_id eq "42")`) split 20/80 on/off, defaulting to
`off`, plus a leading comment, a `version` and `metadata` to prove they survive
a round trip.

`production/growth.goff.yaml` holds `banner-test` and exists to be left alone —
several tests assert it is byte-identical after a commit to the other file.

## Coverage

| File | Covers |
| --- | --- |
| `auth.spec.ts` | Sign-in page, OIDC round trip, both flags listed |
| `toggle.spec.ts` | Review dialog gating, diff reveal, cancel, commit + trailer, untouched-file check |
| `rollout.spec.ts` | Slider values, totals, committed percentages |
| `preview.spec.ts` | Evaluate, default fall-through, invalid JSON |
| `rule-builder.spec.ts` | Loading a query, editing value/attribute/operator, removing a condition, cancel |
| `history.spec.ts` | Empty state, commit appearing after a save |

## Known app bugs

### BUG-1 — the SPA renders nothing at its own flag-list URL

**Status: open. Not fixed here — the fix belongs in `web/src/App.tsx`.**

After signing in, the app redirects to `/env/production`, the sidebar links
there, and flag rows link to `/env/production/flags/<key>` — but **all of those
URLs render an empty page**. The shell (sidebar, protected-env banner) appears
and the content area is blank. No `/api/environments/{env}/flags` request is
made.

The content only renders when the environment segment is repeated:

- `/env/production/env/production` — flag list
- `/env/production/env/production/flags/new-checkout` — detail page

Cause: `App.tsx` mounts `Shell` under a splat route, but the nested `Routes`
inside `Shell` declare absolute paths, so they are matched against the full
pathname rather than the remainder after the splat:

```tsx
// App.tsx
<Route path="/env/:env/*" element={<Shell />} />

// Shell()
<Routes>
  <Route path="/env/:env" element={<FlagListPage … />} />
  <Route path="/env/:env/flags/:key" element={<FlagDetailPage … />} />
</Routes>
```

Nested route paths must be relative to the parent. The likely fix is to make
them relative (`""` and `"flags/:key"`), leaving every link and redirect as-is.

This is purely a routing defect — the API, permissions, diffing, committing,
preview and rule builder all work correctly once the component is mounted.

**How the suite handles it:** `tests/support/studio.ts` exports `listPath()` and
`detailPath()`, which produce the doubled URLs, and navigates directly rather
than clicking through. Two `test.fixme` tests in `auth.spec.ts` pin the intended
behaviour — clicking a flag link, and the post-login URL rendering the list. When
the app is fixed, drop the doubling in those two helpers and remove the `.fixme`;
everything else keeps passing unchanged.

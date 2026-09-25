# Contributing

Thanks for wanting to help. This is a small, deliberately boring codebase and the
goal is to keep it that way.

## Project intent: donation to go-feature-flag

**GO Feature Flag Studio is built to be donated to the
[`go-feature-flag`](https://github.com/go-feature-flag) organisation.** The module
path is already `github.com/go-feature-flag/studio` in anticipation of that
handover.

That has not happened yet, and nothing here is endorsed by or affiliated with the
GO Feature Flag project. Practical consequences for contributors:

- **Keep the licensing clean.** This project is MIT, matching upstream. New
  dependencies must be MIT-, Apache-2.0-, BSD-, or ISC-licensed. No copyleft, no
  Commons Clause, no "source-available" licences, and nothing without an actual
  `LICENSE` file.
- **Do not copy code from repositories without a clear licence.** In particular,
  `go-feature-flag/flag-management` has no licence file in its tree, so it is
  all-rights-reserved — do not copy from it, even though it is the same org.
- **No trademark usage.** Do not add GO Feature Flag logo assets. See `NOTICE`.
- **Sign off your commits** (`git commit -s`). A future donation is much easier
  with clean provenance on every commit.
- Avoid adding a hard dependency on any one company's internal infrastructure.
  Okta is the reference OIDC provider, but the code must stay generic OIDC.

## Getting set up

Requirements: Go (see `go.mod` for the version), Node 20+ for the frontend.

```sh
cp studio.example.yaml studio.yaml
```

For local work you do not need a GitHub App — use a fine-grained PAT with
read/write Contents via `github.devToken`, pointed at a scratch repo. Studio logs
a warning when it starts with a token rather than an App.

Run the backend and the Vite dev server in two terminals. Vite proxies `/api` and
`/auth` to port 8080:

```sh
go run ./cmd/goff-studio -config studio.yaml
cd web && npm run dev
```

## Running tests

```sh
make test     # go test ./... plus a frontend typecheck
make lint     # gofmt -l . and go vet ./...
make build    # frontend build, then the Go binary
```

Individually:

```sh
go test ./...                                  # Go tests
go test ./internal/goff -run TestSerialize -v  # one package, one test
cd web && npx vitest run                       # frontend unit tests
cd web && npx tsc --noEmit -p tsconfig.app.json # typecheck only
cd web && npm run lint                         # oxlint
```

Browser end-to-end tests live in `e2e/` as a **separate npm project and a separate
Go module** (`e2e/harness`), so they do not touch the main module's dependencies:

```sh
cd e2e
npm install
npm run install:browsers   # one-time: fetch Chromium
npm test                   # builds the binary, then runs Playwright
```

Everything must pass before a PR is ready. `make test` is the bar for Go and
typechecking; run the Playwright suite too if you touched the UI or the HTTP API.

### Testing conventions

- Tests are plain `testing`, table-driven where it helps. No assertion library.
- `internal/goff/testdata` holds golden YAML. The byte-identical round-trip test
  is load-bearing — if you change serialisation, understand why it passes before
  you change it.
- `internal/githubapp` tests run against an `httptest` server standing in for the
  GitHub API. Never write a test that talks to real GitHub.
- `internal/server/e2e_test.go` drives the HTTP API end to end in-process with a
  fake GitHub.
- When you fix a bug, add the test that would have caught it.

## Package layout

```
cmd/goff-studio/      main; embeds the built frontend with go:embed
pkg/splits/           public salted-shard experiment evaluator services import; no internal imports
internal/config/      YAML config + GOFF_STUDIO_* env overrides, validation
internal/auth/        OIDC (PKCE) + AES-GCM sealed cookie sessions, no server store
internal/permissions/ group -> file/environment/action matching, default deny
internal/githubapp/   installation tokens, reads, commits, history
internal/storage/     the backend interface and registry, github and file backends
internal/goff/         the GO Feature Flag adapter
internal/server/      HTTP handlers, the service layer, diffs, plain-English summaries
web/                  React 19 + TypeScript + Vite + Tailwind 4 frontend
e2e/                  Playwright suite (separate npm project + Go module)
charts/goff-studio/   Helm chart
backends/s3/          S3 backend, its own Go module and binary
```

Rough dependency direction: `config` and `permissions` are leaves; `pkg/splits`
imports nothing from this repo, because services outside it depend on it;
`goff` depends on nothing internal except `pkg/splits`; `githubapp` depends on nothing internal; `server` wires them
together; `cmd` wires `server`. Keep it that way — if `internal/goff` starts
importing `internal/server`, something has gone wrong.

### Where things go

| Change | Where |
| --- | --- |
| New flag field Studio should edit | `internal/goff/adapter.go` (+ `model.go`) |
| New YAML-preservation behaviour | `internal/goff/yamldoc.go` |
| New query operator | `internal/goff/build.go`, then `web/src/lib/query.ts` |
| New API endpoint | `internal/server/server.go` route + `service.go` logic |
| New permission action | `internal/permissions/permissions.go` + `web/src/lib/api.ts` |
| New config key | `internal/config/config.go` (struct, `applyEnv`, `validate`) + `studio.example.yaml` |

Adding a config key means adding all three of the YAML tag, the `GOFF_STUDIO_*`
override in `applyEnv`, and a line in `studio.example.yaml`. A key that cannot be
set by environment variable is not finished.

## Code conventions

### Go

- `gofmt`, and `go vet` clean. `make lint` checks both.
- **Comments are rare.** Prefer code that does not need them; if a comment is
  genuinely necessary, make it one line explaining *why*, not *what*. Do not add
  doc comments to every exported symbol as a reflex.
- Standard library first. This project has no web framework, no ORM, no assertion
  library, and hand-rolls its GitHub App JWT to avoid a JWT dependency. Adding a
  dependency needs a reason beyond convenience.
- Errors: wrap with `%w` and lowercase context (`fmt.Errorf("reading config: %w", err)`).
  Sentinel errors (`ErrForbidden`, `ErrStaleView`, `ErrFlagConflict`) are compared
  with `errors.Is` and mapped to status codes in one place,
  `server.writeServiceError`.
- **User-facing error strings are written for humans**, not operators: "someone
  else just changed this flag, please reload and try again". Keep that voice, and
  never leak internal detail into a message a business user will read.
- Accept `context.Context` as the first parameter on anything doing I/O.

### Frontend

- TypeScript strict; no `any` in new code except where a third-party control
  genuinely gives you no type (see the `react-querybuilder` custom controls).
- Functional components, hooks. Server state goes through `@tanstack/react-query`;
  do not hand-roll fetch-and-cache.
- All network calls go through the typed `api` object in `web/src/lib/api.ts`. No
  bare `fetch` in components.
- Tailwind utility classes with the semantic tokens already defined (`bg-surface`,
  `text-ink-muted`, `border`), not raw colours. This is what makes dark mode work.
- Keyboard accessibility and `aria-label`s on icon-only controls are not optional.

### The security model is not negotiable

Studio's permission config is the only thing standing between a user and a
production flag. When touching anything in that path:

- **Every permission check happens server-side.** The `actions` array in an API
  response exists to grey out buttons, and is never the thing enforcing anything.
- Permissions **default to deny**. A new action, environment, or code path must be
  unreachable until a rule explicitly allows it.
- Do not add a code path that writes to Git without going through
  `Service.Save`, which checks permissions, validates with GOFF's own validator,
  and handles the stale-SHA and conflict cases.
- Do not weaken the `fileSha` handling. An unrecognised SHA is rejected with a
  409 on purpose.

## Pull requests

- **Title your PR as a conventional commit**, e.g. `feat: add a rename endpoint`
  or `fix(goff): keep progressiveRollout when a rule is edited`. CI enforces
  this, and it matches the convention used by go-feature-flag itself.
- One logical change per PR. A refactor and a behaviour change in the same diff
  will be asked to split.
- Explain the *why* in the description; the diff already shows the what.
- Update the README and `studio.example.yaml` in the same PR as the behaviour change.
- If a hard-won detail about real GO Feature Flag behaviour comes out of your
  work, write it down in the README's "Verified against GO Feature Flag"
  section, with how you measured it. Several of those findings
  are the reason this codebase is shaped the way it is, and re-deriving them is
  expensive.
- Do not commit `studio.yaml`, `*.pem`, tokens, or real org names. `.gitignore`
  covers the common cases but read your own diff.

## Reporting bugs

Include your Studio version or commit, the relevant `studio.yaml` **with secrets
removed**, the flag YAML that triggered it, and what you expected instead. For
anything involving diffs or preservation, the before/after file contents are the
useful part.

Please report suspected security issues privately to the maintainers rather than
in a public issue.

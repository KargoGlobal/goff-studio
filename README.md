# GO Feature Flag Studio

An admin UI for [GO Feature Flag](https://gofeatureflag.org). Business users and
engineers read and edit flag files in a web UI, signing in through your identity
provider.

> **Independent community project.** Studio is not (yet) affiliated with,
> endorsed by, or maintained by the GO Feature Flag project. It is built in the
> hope of being donated to the `go-feature-flag` org, and the module path is
> already `github.com/go-feature-flag/studio` in anticipation. Until that
> handover actually happens, treat this as a third-party tool.

## What it is

- A web UI for reading and editing GO Feature Flag YAML files.
- An OIDC front door.
- A permission layer mapping OIDC groups to files, environments, and actions.
- A writer that produces minimal, reviewable diffs.

## Quickstart

```sh
cp studio.example.yaml studio.yaml   # then edit it
make build                           # builds the frontend, embeds it in the binary
./goff-studio -config studio.yaml    # -config defaults to ./studio.yaml
```

`studio.yaml` is optional: every setting has a `GOFF_STUDIO_*` variable, so a
container needs no file at all. A missing config file is only fatal if you asked
for one with `-config`.

```sh
docker run -p 8080:8080 \
  -e GOFF_STUDIO_BASE_URL=https://studio.example.com \
  -e GOFF_STUDIO_SESSION_SECRET="$(openssl rand -hex 32)" \
  -e GOFF_STUDIO_OIDC_ISSUER_URL=https://your-org.okta.com/oauth2/default \
  -e GOFF_STUDIO_OIDC_CLIENT_ID=... \
  -e GOFF_STUDIO_OIDC_CLIENT_SECRET=... \
  -e GOFF_STUDIO_GITHUB_OWNER=acme \
  -e GOFF_STUDIO_GITHUB_REPO=flags \
  -e GOFF_STUDIO_GITHUB_APP_ID=123 \
  -e GOFF_STUDIO_GITHUB_INSTALLATION_ID=456 \
  -e GOFF_STUDIO_GITHUB_PRIVATE_KEY_PATH=/etc/goff-studio/app.pem \
  -e GOFF_STUDIO_ENVIRONMENTS=dev,production \
  -e GOFF_STUDIO_PERMISSIONS='[{group: flags-admins, allow: ["*"]}]' \
  -v "$PWD/app.pem:/etc/goff-studio/app.pem:ro" \
  ghcr.io/OWNER/REPO:latest
```

Mount a `studio.yaml` at `/etc/goff-studio/studio.yaml` to do it the other way
round; env vars still override anything in the file.

For a shared deployment that is usually the better split: keep `permissions` and
`environments` in a mounted file so policy changes get reviewed like code, and
pass only the secrets as env. A long `GOFF_STUDIO_PERMISSIONS` value works, but it
is awkward to diff and easy to get wrong in a single line.

You need three things before it will start: an OIDC app (Okta, Entra, Auth0,
Keycloak — anything with discovery), somewhere to keep the flag files, and at
least one environment. For local poking, `github.devToken` takes a PAT instead of
a GitHub App.

Two things about the OIDC side catch people out:

- **Register `<server.baseURL>/auth/callback` as the redirect URI.** Studio
  derives it and never sends anything else, so a `baseURL` that does not match
  what your provider has on file fails at login rather than at startup.
- **Groups must arrive in the ID token.** Studio reads `oidc.groupsClaim` from the
  token and does not call UserInfo or any directory API, so a provider that only
  exposes groups separately will authenticate users into zero groups — which,
  with default-deny permissions, looks like "signed in but nothing is visible".
  Providers differ here: Entra usually needs a groups claim switched on, and
  Auth0 namespaces custom claims.

Requested scopes are currently fixed at `openid profile email groups` and are not
configurable.

Every config key can be overridden with a `GOFF_STUDIO_*` environment variable,
which is how you should inject secrets. For the two list-shaped keys,
`GOFF_STUDIO_ENVIRONMENTS` and `GOFF_STUDIO_PERMISSIONS`, the variable replaces
the file's list outright rather than merging into it.

### Server and auth

| Config key | Env var | Notes |
| --- | --- | --- |
| `server.addr` | `GOFF_STUDIO_ADDR` | default `:8080` |
| `server.baseURL` | `GOFF_STUDIO_BASE_URL` | default `http://localhost:8080`; the OIDC redirect is `<baseURL>/auth/callback` |
| `server.sessionSecret` | `GOFF_STUDIO_SESSION_SECRET` | **required**, min 16 characters |
| `server.secureCookies` | `GOFF_STUDIO_SECURE_COOKIES` | set `true` behind HTTPS |
| `oidc.issuerURL` | `GOFF_STUDIO_OIDC_ISSUER_URL` | **required**; no query or fragment, discovery appends `/.well-known/openid-configuration` |
| `oidc.clientID` | `GOFF_STUDIO_OIDC_CLIENT_ID` | **required** |
| `oidc.clientSecret` | `GOFF_STUDIO_OIDC_CLIENT_SECRET` | **required**; Studio is a confidential client |
| `oidc.groupsClaim` | `GOFF_STUDIO_OIDC_GROUPS_CLAIM` | default `groups`; the ID token claim matched against `permissions[].group` |
| `expectedPollSeconds` | `GOFF_STUDIO_EXPECTED_POLL_SECONDS` | default 60; only feeds the "live in about N seconds" message |

### Storage

`storage.backend` picks where flag files live. It defaults to `github`, the only
backend with history, attribution and review.

| Config key | Env var | Notes |
| --- | --- | --- |
| `storage.backend` | `GOFF_STUDIO_STORAGE` | `github` (default), `file`, or `s3` |
| `environments` | `GOFF_STUDIO_ENVIRONMENTS` | `dev,production`, or YAML for `display`/`protected`/`order`; at least one required |
| `discoverEnvironments` | `GOFF_STUDIO_DISCOVER_ENVIRONMENTS` | `true` finds directories itself, instead of listing them |
| `permissions` | `GOFF_STUDIO_PERMISSIONS` | YAML or JSON list, e.g. `[{group: admins, allow: ["*"]}]` |

**`github`** — commits as a GitHub App, so every change is reviewable history.

| Config key | Env var | Notes |
| --- | --- | --- |
| `github.owner` | `GOFF_STUDIO_GITHUB_OWNER` | **required** |
| `github.repo` | `GOFF_STUDIO_GITHUB_REPO` | **required** |
| `github.branch` | `GOFF_STUDIO_GITHUB_BRANCH` | default `main` |
| `github.appID` | `GOFF_STUDIO_GITHUB_APP_ID` | with `installationID` + `privateKeyPath` |
| `github.installationID` | `GOFF_STUDIO_GITHUB_INSTALLATION_ID` | |
| `github.privateKeyPath` | `GOFF_STUDIO_GITHUB_PRIVATE_KEY_PATH` | PEM, RSA (PKCS#1 or PKCS#8) |
| `github.devToken` | `GOFF_STUDIO_GITHUB_DEV_TOKEN` | **local dev only**, substitutes for the App |

**`file`** — writes straight to a directory. No history, attribution or review, so
it is for local development.

| Config key | Env var | Notes |
| --- | --- | --- |
| `storage.path` | `GOFF_STUDIO_STORAGE_PATH` | **required**; the directory holding your environment directories |

**`s3`** — needs the separate `goff-studio-s3` binary and image, so the AWS SDK
stays out of the core build. History needs bucket versioning; Studio checks at
startup and hides the history panel if it is off. Credentials come from the
standard AWS chain.

| Config key | Env var | Notes |
| --- | --- | --- |
| `storage.bucket` | `GOFF_STUDIO_STORAGE_BUCKET` | **required** |
| `storage.region` | `GOFF_STUDIO_STORAGE_REGION` | |
| `storage.prefix` | `GOFF_STUDIO_STORAGE_PREFIX` | optional key prefix |
| `storage.options` | `GOFF_STUDIO_STORAGE_OPTIONS` | YAML map; `{endpoint: "http://minio:9000"}` for S3-compatible storage |

## Flag file layout

```
flags-repo/
  dev/
    payments.goff.yaml
    growth.goff.yaml
  production/
    payments.goff.yaml
    growth.goff.yaml
```

Environments are top-level directories, and inside each one file per team named
after the team: `production/growth.goff.yaml` is the team `growth`. That pairs
naturally with CODEOWNERS, and a save only ever rewrites the one file it touched.
Flag keys must be unique across all files in an environment — Studio reports
duplicates instead of letting one silently win.

When you create a flag you pick a team, not a file; Studio derives the path and
writes the team to `metadata.team` so it is explicit in the YAML rather than
implied by the filename. "New team" creates `<env>/<team>.goff.yaml` with a seed
comment — Studio never invents a file as a side effect of saving a flag. A flag
whose `metadata.team` is missing shows as unassigned, which is what you see for
files written by hand before Studio.

## Permission model

Permissions map OIDC groups to file patterns, environments, and actions.

```yaml
permissions:
  - group: flags-admins
    allow: ["*"]
  - group: marketing
    allow: ["growth"]
    environments: [production]
    actions: [toggle, rollout]
```

| Action | Grants |
| --- | --- |
| `view` | read a flag |
| `toggle` | turn a flag on or off |
| `rollout` | change percentage splits |
| `edit_rules` | change targeting queries |
| `edit_variations` | change variation values |
| `create` | add a new flag |
| `delete` | remove a flag |

Rules:

- **Default deny.** No matching rule means no access; an empty `permissions` list
  denies everything.
- **Any action implies `view`.** Granting `toggle` also grants read.
- Omitting `actions` grants all of them; omitting `environments` matches all.
- `group: "*"` matches every signed-in user.
- `allow` patterns match the file path (`production/growth.goff.yaml`), the
  basename with the extension stripped (`growth`), or a glob of either — so
  `allow: ["growth"]` is the normal way to say "the growth team's file, in
  whichever environment this rule covers".
- `create` is checked against the derived path, so a user who may not write
  `production/billing.goff.yaml` can neither create a flag in team `billing` nor
  create the team itself. The team dropdown only offers what you may create in.

## How a change reaches production

1. A user signs in via OIDC. Studio reads groups from the `groups` claim.
2. They change a flag. Studio checks its own permission rules, server-side.
3. Studio re-reads the file, applies the change, and validates the result with
   GO Feature Flag's own parser.
4. It commits straight to `github.branch` (default `main`) as the GitHub App,
   with the signed-in user as the commit author and recorded in commit trailers:

   ```
   [production] growth/new-checkout: enabled

   GOFF-Studio-User: someone@example.com
   GOFF-Studio-User-Id: 00u1abc...
   ```

5. Apps pick the change up on their next poll.

There are no pull requests; commits go directly to the branch.

## Concurrency

Before writing, Studio re-reads the file.

| Situation | Result |
| --- | --- |
| File unchanged | commit |
| File changed, but not your flag | re-apply to fresh content and commit, up to 3 attempts; the user never sees it |
| Your flag changed underneath you | `409`, "someone else just changed this flag" |
| Client sends a `fileSha` Studio never issued | `409` rather than trusted |

That last one matters: an unrecognised SHA means the client is working from a
view Studio cannot verify, so committing anyway could clobber whatever changed
in between.

## What it edits, and what it preserves

| Edited | Preserved untouched |
| --- | --- |
| `variations` | `version` |
| `targeting` | `trackEvents` |
| `defaultRule` | `bucketingKey` |
| `disable` | `scheduledRollout` |
| `experimentation` | per-rule `progressiveRollout` |
| `metadata` | any field a future GO Feature Flag release adds |

Preserved fields surface as read-only in the UI, and the adapter has tests
proving they survive an edit.

**Minimal-diff guarantee:** a save that touches one flag produces a diff that
touches only that flag. Comments, key order, and quoting all survive.

## Verified against GO Feature Flag, not assumed

- **Validation** uses GOFF's own `dto.DTO.Convert()` then `IsValid()` — exactly
  what its linter (`cmd/cli/linter/linter.go`) does. Studio never commits a file
  that fails it.
- **Preview** runs the real engine over an in-memory retriever fed the unsaved
  draft, so preview matches production.
- **Percentages do not have to sum to 100.** GOFF's validation only rejects an
  empty or all-zero percentage map — sums of 20, 200, negative values, and
  fractions all pass. A UI forcing a sum of exactly 100 is wrong.
- **Percentages behave as relative weights, not absolute percentages.** A rule
  with `a: 10, b: 10` splits matching users ~50/50; it does *not* leave 80%
  falling through to the next rule. Once a rule's query matches, evaluation stops
  there. To let users miss a rule entirely, put a control variation in the split.
  Measured against v1.55.3 with 2000 targeting keys per case.
- **`experimentation` is a scheduled kill switch, not a targeting rule.** GOFF
  evaluates `IsDisable() || isExperimentationOver(date)` in one condition
  (`flag/internal_flag.go`), and both return `ReasonDisabled`. Outside the window
  the flag is off and everyone gets the default value; evaluation does *not* fall
  through to targeting. Studio therefore presents it as **Schedule** next to the
  on/off state, and a flag whose window has closed is badged `scheduled` or
  `expired` rather than reported as on.
- **`yaml.v3` cannot round-trip byte-for-byte.** It drops the blank line before a
  comment even on an untouched re-encode. Studio edits the node tree for
  correctness, then splices only the changed flag's lines back into the original
  text. That is what makes the byte-identical test pass.

## Development

```sh
make test      # go test -race across all modules, then typecheck and frontend tests
make lint      # gofmt, go vet, golangci-lint, then eslint
make build     # frontend build, then the Go binary
```

For frontend work, run the Go server and Vite side by side. Vite proxies `/api`
and `/auth` to port 8080:

```sh
go run ./cmd/goff-studio -config studio.yaml
cd web && npm run dev
```

Frontend unit tests use Vitest:

```sh
cd web && npx vitest run
```

Stack: Go 1.27 standard-library HTTP (`net/http` routing patterns, no web
framework), React 19 + TypeScript + Vite + Tailwind 4, `react-querybuilder` for
the rule builder. The built frontend is embedded in the binary with `go:embed`,
so deployment is one static binary plus a config file.

See [CONTRIBUTING.md](CONTRIBUTING.md) for package layout and conventions.

## HTTP API

All `/api` routes require a session cookie and return `401` without one.

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/healthz` | liveness, no auth |
| `GET` | `/auth/login` | start the OIDC flow (PKCE) |
| `GET` | `/auth/callback` | OIDC redirect target |
| `POST` | `/auth/logout` | clear the session cookie |
| `GET` | `/api/me` | user, groups, visible environments, poll seconds, backend capabilities |
| `GET` | `/api/environments/{env}/flags` | flag list, selectable teams, unparseable/duplicate flags |
| `POST` | `/api/environments/{env}/flags` | create a flag in a team |
| `GET` | `/api/environments/{env}/flags/{key}` | one flag |
| `DELETE` | `/api/environments/{env}/flags/{key}` | delete a flag |
| `PUT` | `/api/environments/{env}/flags/{key}/variations` | replace variations and the default |
| `POST` | `/api/environments/{env}/flags/{key}/state` | toggle on/off |
| `POST` | `/api/environments/{env}/flags/{key}/key` | rename a flag |
| `POST` | `/api/environments/{env}/flags/{key}/rollout` | set percentages |
| `POST` | `/api/environments/{env}/flags/{key}/progressive` | set or clear a progressive rollout |
| `POST` | `/api/environments/{env}/flags/{key}/experimentation` | set or clear the experimentation window |
| `POST` | `/api/environments/{env}/flags/{key}/rule` | set a rule's targeting query |
| `POST` | `/api/environments/{env}/flags/{key}/rules` | add a rule |
| `DELETE` | `/api/environments/{env}/flags/{key}/rules/{rule}` | delete a rule |
| `PUT` | `/api/environments/{env}/flags/{key}/rules/order` | reorder rules |
| `POST` | `/api/environments/{env}/flags/{key}/preview` | evaluate against the live engine |
| `POST` | `/api/environments/{env}/flags/{key}/diff` | plain-language description + unified diff, no write |
| `GET` | `/api/environments/{env}/flags/{key}/history` | commits touching this flag |
| `GET` | `/api/environments/{env}/attributes` | attribute names seen in existing rules |
| `POST` | `/api/environments` | create an environment directory |
| `POST` | `/api/environments/{env}/teams` | create a team file |

Error codes: `403` no permission, `404` unknown flag, `409` stale view or
concurrent edit on the same flag.

## Project status

Working today: the HTTP API, GOFF adapter, OIDC auth, permissions, and the write
path across all three storage backends. The React UI covers the flag list with
search, team filter and inline toggles; flag detail with variations, percentage
sliders and a visual rule builder including negated and nested condition groups;
creating, renaming and deleting flags and teams; editing progressive rollouts and
the schedule (`experimentation`); review-before-save with a real file diff; live
preview; and per-flag history.
Light and dark mode, keyboard accessible, protected environments called out and
requiring typed confirmation.

437 tests pass: 292 Go tests plus 14 in the S3 module, 78 frontend tests, and 53
Playwright tests driving the real binary against fake OIDC and GitHub servers.
`make lint` runs `gofmt`, `go vet` and `golangci-lint` with the same linter set
go-feature-flag uses on itself. A `Dockerfile`, a Helm chart under
`charts/goff-studio`, and the end-to-end harness under `e2e/` are in the tree.

Known gaps:

- **Scheduled rollout is view-only.** Steps are preserved byte-for-byte and
  listed under "advanced fields", but Studio will not edit them. A step is a full
  flag overlay, so a partial editor risks dropping fields.
- **`bucketingKey` is view-only.** It is preserved, but you cannot set it from the
  UI.
- **No cross-environment view or promote.** You cannot yet compare dev against
  production side by side, or copy a flag between environments.
- **Rules reorder with up/down buttons**, not drag and drop.
- **Sessions expire after 12 hours** and re-prompt; there is no refresh-token
  rotation, and a sealed cookie cannot be revoked early.

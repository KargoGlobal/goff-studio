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
  -e GOFF_STUDIO_PROTECTED_ENVIRONMENTS=production \
  -e GOFF_STUDIO_PERMISSIONS='[{group: flags-admins, teams: ["*"]}]' \
  -v "$PWD/app.pem:/etc/goff-studio/app.pem:ro" \
  ghcr.io/OWNER/REPO:latest
```

Mount a `studio.yaml` at `/etc/goff-studio/studio.yaml` to do it the other way
round; env vars still override anything in the file.

For a shared deployment that is usually the better split: keep `permissions` and
`protectedEnvironments` in a mounted file so policy changes get reviewed like code, and
pass only the secrets as env. A long `GOFF_STUDIO_PERMISSIONS` value works, but it
is awkward to diff and easy to get wrong in a single line.

You need two things before it will start: an OIDC app (Okta, Entra, Auth0,
Keycloak — anything with discovery) and somewhere to keep the flag files.
Environments are found from storage, so an empty repository is fine; the first
person allowed to create one is offered a "Create your first environment" button.
For local poking, `github.devToken` takes a PAT instead of a GitHub App.

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
`GOFF_STUDIO_PROTECTED_ENVIRONMENTS` and `GOFF_STUDIO_PERMISSIONS`, the variable
replaces the file's list outright rather than merging into it.

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
| `layout` | `GOFF_STUDIO_LAYOUT` | `team-files` (default) or `single-file`; see [Flag file layout](#flag-file-layout) |

### Storage

`storage.kind` picks where flag files live, using the same names as GO Feature
Flag's retriever `kind`. It defaults to `github`, the only kind with review.
Every kind is built into the binary and the image.

| Kind | History | Attribution | Review |
| --- | --- | --- | --- |
| `github` | yes, commits | yes, commit author and trailers | yes, via CODEOWNERS |
| `s3` | with bucket versioning | with bucket versioning, in object metadata | no |
| `googleStorage` | with object versioning | with object versioning, in object metadata | no |
| `azureBlobStorage` | with blob versioning | with blob versioning, in blob metadata | no |
| `configmap` | no | no | no |
| `file` | no | no | no |

Studio reports these as capabilities to the UI: history shows the per-flag
history panel, attribution means each history entry names who made the change
and with what message, and review means changes can be gated by CODEOWNERS. The
UI hides or rewords anything a backend cannot do.

Names match without regard to case. Releases before this one used `storage.backend`
and the names `gcs` and `azblob`; all three still work, with a startup warning
asking you to rename them.

To build a smaller binary, leave backends out with build tags:
`go build -tags no_googlestorage,no_azureblobstorage ./cmd/goff-studio`. The tags
are `no_s3`, `no_googlestorage`, `no_azureblobstorage` and `no_configmap`; the
Dockerfile takes the same list as the `GO_TAGS` build argument. `github` and
`file` are always included.

| Config key | Env var | Notes |
| --- | --- | --- |
| `storage.kind` | `GOFF_STUDIO_STORAGE` | `github` (default), `file`, `s3`, `googleStorage`, `azureBlobStorage`, or `configmap` |
| `protectedEnvironments` | `GOFF_STUDIO_PROTECTED_ENVIRONMENTS` | `production,eu-production` or a YAML list; changes there need a diff review and typed confirmation. A name with no folder yet is fine |
| `teams` | `GOFF_STUDIO_TEAMS` | YAML or JSON list, e.g. `[{name: growth, editors: [marketing]}]`; see [Teams](#teams). Optional |
| `permissions` | `GOFF_STUDIO_PERMISSIONS` | YAML or JSON list, e.g. `[{group: admins, teams: ["*"]}]` |

Upgrading from a release with `environments` or `discoverEnvironments`
(`GOFF_STUDIO_ENVIRONMENTS`, `GOFF_STUDIO_DISCOVER_ENVIRONMENTS`): Studio refuses
to start while they are set. Delete them and list the entries that had
`protected: true` in `protectedEnvironments`. `display` and `order` are gone; the
UI shows folder names as they are, in alphabetical order.

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

**`s3`** — credentials come from the standard AWS chain.

S3 can be the primary store with no Git repository or sync job behind it. Every
object Studio writes carries who made the change and why, as S3 user metadata:

| Metadata key | Value |
| --- | --- |
| `x-amz-meta-studio-user-name` | the signed-in user's name |
| `x-amz-meta-studio-user-email` | their email |
| `x-amz-meta-studio-user-id` | their OIDC subject |
| `x-amz-meta-studio-message` | the same message a GitHub commit gets, e.g. `[production] growth/new-checkout: enabled` |

S3 metadata must be ASCII and is limited to 2 KB per object, so printable ASCII
is stored as-is and anything else (accented names, emoji) is stored as an RFC
2047 encoded word, `=?UTF-8?B?<base64>?=`, which Studio decodes. Name, email and
id are capped at 256 bytes each, and the message is truncated with `...` to fit
what remains.

**Turn on bucket versioning.** Metadata lives on each object version, so history
and attribution need versioning; Studio checks at startup and hides the history
panel if it is off. With versioning, the history panel lists each version newest
first with its author, email and message (one `HeadObject` per version shown, a
few at a time). Versions written before Studio recorded attribution show an
unknown author. Studio never deletes flag files, so it never creates delete
markers; a delete made outside Studio appears in history with an unknown author,
because S3 delete markers cannot carry metadata. For an audit trail that cannot
be rewritten, also consider S3 Object Lock with a retention period, and a
lifecycle rule to expire noncurrent versions you no longer need.

| Config key | Env var | Notes |
| --- | --- | --- |
| `storage.bucket` | `GOFF_STUDIO_STORAGE_BUCKET` | **required** |
| `storage.region` | `GOFF_STUDIO_STORAGE_REGION` | |
| `storage.prefix` | `GOFF_STUDIO_STORAGE_PREFIX` | optional key prefix |
| `storage.options` | `GOFF_STUDIO_STORAGE_OPTIONS` | YAML map; `{endpoint: "http://minio:9000"}` for S3-compatible storage |

**`googleStorage`** — Google Cloud Storage. Credentials come from Google
Application Default Credentials, so Workload Identity works with no extra configuration.

Writes are conditional on the object's generation, so a stale write is rejected
rather than clobbering. Every object Studio writes carries the same attribution
as S3 (`studio-user-name`, `studio-user-email`, `studio-user-id`,
`studio-message`) as GCS custom metadata; GCS accepts UTF-8 there, so nothing is
encoded. Turn on object versioning for history and attribution; Studio checks at
startup and hides the history panel if it is off. See
[backends/gcs/README.md](backends/gcs/README.md).

| Config key | Env var | Notes |
| --- | --- | --- |
| `storage.bucket` | `GOFF_STUDIO_STORAGE_BUCKET` | **required** |
| `storage.prefix` | `GOFF_STUDIO_STORAGE_PREFIX` | optional object name prefix |
| `storage.options` | `GOFF_STUDIO_STORAGE_OPTIONS` | YAML map; `{endpoint: "http://localhost:4443"}` for an emulator (sent without credentials) |

**`azureBlobStorage`** — Azure Blob Storage. Studio authenticates with `DefaultAzureCredential`
(managed identity, Workload Identity, or the Azure CLI locally), or with a
connection string in `AZURE_STORAGE_CONNECTION_STRING`, which takes precedence
and is how you reach the Azurite emulator. The connection string carries the
account key, so it is read from the environment, never from the config file.

Writes use `If-Match` on the blob's ETag, and creates use `If-None-Match: *`.
Attribution is stored as blob metadata (`studio_user_name`,
`studio_user_email`, `studio_user_id`, `studio_message`; Azure requires
identifier-style names), encoded like S3's. Turn on blob versioning for history
and attribution. See [backends/azblob/README.md](backends/azblob/README.md).

| Config key | Env var | Notes |
| --- | --- | --- |
| `storage.bucket` | `GOFF_STUDIO_STORAGE_BUCKET` | **required**; the container name |
| `storage.prefix` | `GOFF_STUDIO_STORAGE_PREFIX` | optional blob name prefix |
| `storage.options` | `GOFF_STUDIO_STORAGE_OPTIONS` | YAML map; `{accountURL: "https://<account>.blob.core.windows.net"}`, required unless `AZURE_STORAGE_CONNECTION_STRING` is set |

**`configmap`** — Kubernetes ConfigMaps. Each environment is one ConfigMap named `<prefix><environment>`, and each team file
is one key in it, which is the shape GO Feature Flag's Kubernetes retriever
reads. Studio uses its pod's service account; writes are conditional on the
ConfigMap's `resourceVersion`, so a stale write conflicts rather than
clobbering. There is no history, attribution or review. See
[backends/configmap/README.md](backends/configmap/README.md).

| Config key | Env var | Notes |
| --- | --- | --- |
| `storage.prefix` | `GOFF_STUDIO_STORAGE_PREFIX` | prepended to the environment name to form the ConfigMap name |
| `storage.options` | `GOFF_STUDIO_STORAGE_OPTIONS` | YAML map; `namespace` (default: Studio's own), and `apiServer` for `kubectl proxy` in local development |

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

Environments are discovered, not configured: every top-level directory holding
at least one `.yaml`/`.yml` file is an environment, shown under its folder name
and sorted alphabetically. A directory with no flag files (say `docs/`) is not
one, and dot-directories are ignored. The list is cached for 30 seconds, and
creating an environment in Studio refreshes it straight away; a folder added
outside Studio shows up within the cache window. Inside each environment there
is one file per team named after the team: `production/growth.goff.yaml` is the
team `growth`, and `production/flags.goff.yaml` holds flags with no team. That pairs
naturally with CODEOWNERS, and a save only ever rewrites the one file it touched.
Flag keys must be unique across all files in an environment — Studio reports
duplicates instead of letting one silently win.

When you create a flag you pick one of the [declared teams](#teams) or "No team",
not a file; Studio derives the path and also writes the team to `metadata.team`.
The first flag for a team creates `<env>/<team>.goff.yaml` with a seed comment;
that is the only time Studio creates a file other than an environment's
`flags.goff.yaml`. In this layout the file decides the team, so a hand-edited
`metadata.team` that disagrees with the file is ignored.

### Single-file layout

With `layout: single-file`, each environment is one file, `<env>/flags.goff.yaml`,
and a flag's team is only its `metadata.team`:

```
flags-store/
  dev/flags.goff.yaml
  production/flags.goff.yaml
```

This suits object storage, where the relay proxy needs one retriever per file:
adding a team needs no relay change, only adding an environment does.

- Permissions behave as if each team still had its own file. `teams: ["growth"]`
  matches flags whose `metadata.team` is `growth`, and a flag with no team matches
  only `*` rules.
- Moving a flag to another team (editing `metadata.team`) needs `delete` on the old
  team and `create` on the new one, checked against the file as it is written.
- Every save in an environment touches the same file, so two saves at the same
  moment are more likely to rebase or, when they touch the same flag, return 409.
- CODEOWNERS cannot tell teams apart, so prefer `team-files` with the GitHub backend.

### Flag timestamps

Studio writes `metadata.createdAt` when it creates a flag and `metadata.updatedAt` on
every save, both as RFC 3339 UTC. Flags created outside Studio have no `createdAt`, and
the flag list shows "—" for any missing date. Promotion never copies these keys
between environments, and Compare does not count them as drift.

## Teams

Teams are declared in `studio.yaml` and nowhere else; the UI cannot add one. Each
team names the OIDC groups that edit its flags, optionally limited to some
environments and actions:

```yaml
teams:
  - name: payments
    editors: [payments-team]
  - name: growth
    editors: [marketing]
    environments: [production]
    actions: [toggle, rollout]
```

- The team dropdown offers the declared teams you may create in, plus "No team"
  if you have a `"*"` rule. A save or create naming any other team is refused.
- `teams` is optional. Without it there is no team dropdown, every flag has no
  team, and access comes from `"*"` rules in `permissions`.
- A flag whose team is not declared (a removed team, or a hand-edited file) still
  loads, marked "unknown team", and only `"*"` rules reach it. Re-declare the team
  or move the flag to restore access.
- Names follow the environment-name rules (letters, digits, `.`, `-`, `_`), must
  be unique, and cannot be `flags`, which is the no-team file. Studio refuses to
  start otherwise, and warns about any `permissions` rule naming an undeclared team.

## Permission model

Each team's `editors` become rules internally. `permissions` holds the rest:
admins, read-only access, and anything spanning teams.

```yaml
permissions:
  - group: flags-admins
    teams: ["*"]
  - group: "*"
    teams: ["*"]
    actions: [view]
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
- `teams` lists team names or `"*"`. Globs and paths are rejected. `"*"` is the
  only way to reach flags with no team or an unknown team.
- `create` is checked against the chosen team, and moving a flag between teams
  needs `delete` on the old one and `create` on the new one.
- Creating an environment is checked as `create` in that environment name. The
  UI only offers "New environment" to groups with a `create` rule that has no
  `environments` restriction (or `"*"`), since the name is not known up front.

### Input validation

Every value that becomes part of a file path or is written into YAML is validated
server-side, in `internal/server/validate.go`:

- **Path segments**: environment names reject path separators, `..`, leading dots
  and whitespace, so nothing the client sends can escape the environment directory
  it belongs to. Team names must be declared in config, so a client cannot choose
  a file name at all.
- **Anything embedded in YAML** rejects line breaks and control characters,
  including U+2028 and U+2029, which a YAML parser treats as line breaks even
  though Go does not consider them control runes.
- **Percentages** are bounded to 0–100 individually and must not all be zero;
  GOFF's own validator accepts negative and >100 shares.
- **Names and queries** have length limits, and a client-supplied history limit is
  clamped before it reaches a backend that would overflow it.
- **Request bodies** are capped at 1 MiB.
- Existence checks for rules and variations run *after* the permission check, so a
  caller who may not edit a flag learns nothing about its contents from an error
  message.

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
  fractions all pass. Studio's sliders always total 100 and show hand-written
  weights as their real share; the file is only rewritten when you save a split.
- **Percentages behave as relative weights, not absolute percentages.** A rule
  with `a: 10, b: 10` splits matching users ~50/50; it does *not* leave 80%
  falling through to the next rule. Once a rule's query matches, evaluation stops
  there. To let users miss a rule entirely, put a control variation in the split.
  Measured against v1.55.3 with 2000 targeting keys per case.
- **Promotion never turns a flag on by itself.** Copying settings between
  environments leaves the target's own on/off state alone, and a flag that does not
  exist in the target yet is created disabled. The state is copyable, but only when
  it is ticked explicitly. A progressive rollout is frozen into the percentage split
  it has reached *at the moment you promote*, interpolated between its two steps,
  because the source environment's ramp dates mean nothing in the target. Freezing
  at the end allocation instead would silently fast-forward a half-finished rollout
  to 100% in production. Promoting rules that serve a
  variation the target lacks is refused before anything is written.
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
| `GET` | `/api/me` | user, groups, visible environments, whether you may create one, poll seconds, backend capabilities |
| `GET` | `/api/environments/{env}/flags` | flag list, selectable teams, unparseable/duplicate flags |
| `POST` | `/api/environments/{env}/flags` | create a flag in a declared team, or with no team |
| `GET` | `/api/environments/{env}/flags/{key}` | one flag |
| `DELETE` | `/api/environments/{env}/flags/{key}` | delete a flag |
| `PUT` | `/api/environments/{env}/flags/{key}/variations` | replace variations and the default |
| `POST` | `/api/environments/{env}/flags/{key}/state` | toggle on/off |
| `POST` | `/api/environments/{env}/flags/{key}/key` | rename a flag |
| `POST` | `/api/environments/{env}/flags/{key}/rollout` | set percentages |
| `POST` | `/api/environments/{env}/flags/{key}/progressive` | set or clear a progressive rollout |
| `POST` | `/api/environments/{env}/flags/{key}/experimentation` | set or clear the experimentation window |
| `GET` | `/api/flags/{key}/compare?from=&to=` | compare one flag across two environments |
| `POST` | `/api/flags/{key}/promote/diff` | preview a promotion |
| `POST` | `/api/flags/{key}/promote` | copy selected settings between environments |
| `POST` | `/api/environments/{env}/flags/{key}/rule` | set a rule's targeting query |
| `POST` | `/api/environments/{env}/flags/{key}/rules` | add a rule |
| `DELETE` | `/api/environments/{env}/flags/{key}/rules/{rule}` | delete a rule |
| `PUT` | `/api/environments/{env}/flags/{key}/rules/order` | reorder rules |
| `POST` | `/api/environments/{env}/flags/{key}/preview` | evaluate against the live engine |
| `POST` | `/api/environments/{env}/flags/{key}/diff` | plain-language description + unified diff, no write |
| `GET` | `/api/environments/{env}/flags/{key}/history` | commits touching this flag |
| `GET` | `/api/environments/{env}/attributes` | attribute names seen in existing rules |
| `POST` | `/api/environments` | create an environment directory |
| `GET` | `/api/teams` | declared teams you can see, their editor groups, and flag counts per environment |

Error codes: `403` no permission, `404` unknown flag, `409` stale view or
concurrent edit on the same flag.

## Project status

Working today: the HTTP API, GOFF adapter, OIDC auth, permissions, and the write
path across all six storage backends. The React UI covers the flag list with
search, team filter and inline toggles; flag detail with variations, percentage
sliders and a visual rule builder including negated and nested condition groups;
creating, renaming and deleting flags; editing progressive rollouts and
the schedule (`experimentation`); comparing one flag across two environments and
promoting selected settings between them; review-before-save with a real file
diff; live preview; and per-flag history.
Light and dark mode, keyboard accessible, protected environments called out and
requiring typed confirmation.

486 tests pass: 337 Go tests plus 14 in the S3 module, 78 frontend tests, and 57
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
- **Rules reorder with up/down buttons**, not drag and drop.
- **Sessions expire after 12 hours** and re-prompt; there is no refresh-token
  rotation, and a sealed cookie cannot be revoked early.

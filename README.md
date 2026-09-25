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
- An editor for salted-shard experiment splits kept in flag metadata, plus
  `pkg/splits`, the Go evaluator services import to serve them.

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
backend with review.

| Backend | History | Attribution | Review |
| --- | --- | --- | --- |
| `github` | yes, commits | yes, commit author and trailers | yes, via CODEOWNERS |
| `s3` | with bucket versioning | with bucket versioning, in object metadata | no |
| `file` | no | no | no |

Studio reports these as capabilities to the UI: history shows the per-flag
history panel, attribution means each history entry names who made the change
and with what message, and review means changes can be gated by CODEOWNERS. The
UI hides or rewords anything a backend cannot do.

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
stays out of the core build. Credentials come from the standard AWS chain.

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

## Experiments

Studio also keeps an experiment registry and a metric catalog next to your flags,
and shows results computed by an external analysis service. Everything lives in
the same repository and goes through the same review-and-commit path as flags.

```
flags-repo/
  production/
    bidder.goff.yaml          # the flag the experiment runs on
  experiments/
    tmax-exp-us-east-1.yaml   # one registry entry per experiment
  metrics/
    dsp_bid_rate.yaml         # one catalog entry per metric
```

`experiments/` and `metrics/` are reserved: environment discovery skips them and
you cannot create an environment with either name. See [`examples/`](examples/)
for a complete, valid set you can point the `file` backend at.

**Registry entry** (`experiments/<key>.yaml`): key, name, owner team, hypothesis,
optional ticket, the flag and environment, the flag rules it runs in
(`allocations`), `control` and `variants` (which must be variations of the
flag), the randomization unit (`request` or `entity`), `start` and a required
`end`, `status` (`draft`, `running`, `stopped`, `concluded`), primary, secondary
and guardrail metrics (guardrails carry a `max_drop_pct` tolerance), analysis
options (`sequential` or `fixed`, `alpha`, `power`, CUPED with a covariate,
`none`/`holm`/`bh` correction, strata), result segments, and an optional
recorded decision. An experiment may run for at most 8 weeks unless it is marked
`extended: true`. Keys Studio does not know about are preserved on save.

The registry key has to be the experiment key one of its listed allocations
logs exposures under: the allocation's `experimentKey` from
`metadata.experiment`, or `<flag>-<rule>` for a rule without one (a plain
percentage rollout included). That keeps keys unforgeable: a team cannot
register an entry under another team's experiment key and read its results.

**Metric catalog entry** (`metrics/<key>.yaml`): `kind` is `mean` (a numerator
column) or `ratio` (numerator and denominator columns), with a display `format`
(`percent`, `currency`, `number`), the `direction` that counts as better, and an
optional winsorization `cap`.

### Permissions for experiments

No new actions. An experiment is owned by its flag's team file, so the flag's
rules apply:

| Operation | Checked as |
| --- | --- |
| list, read, results | `view` on the flag's file in the experiment's environment |
| create | `create` on the flag's file |
| edit (including status and decision) | `edit_rules` on the flag's file, and on the previous flag's file if the flag changed |

If the flag cannot be found, the owner team's file (`<env>/<owner>.goff.yaml`)
stands in, so an orphaned entry stays default-deny. The metric catalog is
shared: anyone who can see an environment can read it, and writes are checked as
`create` or `edit_rules` against `metrics/<key>.yaml` with **no environment**, so
only rules without an `environments` list (or with `"*"`) grant them, for
example `{group: analysts, allow: ["metrics/*"], actions: [create, edit_rules]}`.

### Analysis service

| Config key | Env var | Notes |
| --- | --- | --- |
| `analysis.baseURL` | `GOFF_STUDIO_ANALYSIS_BASE_URL` | optional; without it Studio serves sample results |
| `analysis.token` | `GOFF_STUDIO_ANALYSIS_TOKEN` | sent as `Authorization: Bearer <token>` |

Studio calls two endpoints on the service:

- `GET {baseURL}/v1/experiments/{key}/results?segments=true[&as_of=<date>]`
  returns the results document: `experiment_key`, `as_of`, `status`
  (`ok`, `insufficient_data`, `error`), `method`, `variants` with units and
  expected share, `srm` (chi-square, p-value, `flag`), `metrics` with per-variant
  `value`, `control_value`, relative `lift` and its interval (`ci_low`,
  `ci_high`), `p_value`, `adjusted_p`, `significant`, `raw`, `cuped` and
  `guardrail`, plus `segments`, a daily cumulative-lift
  `timeseries`, `diagnostics`, and a `decision` recommendation
  (`roll_out`, `discuss`, `do_not_roll_out`, `keep_running`).
  When CUPED is enabled and applies to a metric, the top-level estimates
  (`value` through `significant`) are the CUPED-adjusted ones, `raw` carries the
  unadjusted `value`, `control_value`, `lift`, `ci_low`, `ci_high` and
  `p_value`, and `cuped` mirrors the adjusted estimate with its
  `variance_reduction`; both are null otherwise. The UI marks adjusted lifts
  "CUPED" and shows the unadjusted readout alongside. Documents without `raw`
  are read the older way (top level unadjusted, adjusted in `cuped`). Lift,
  interval and p-values are null when the control mean is zero or negative, and
  a guardrail may come back as `{pass: false, significant_harm: false, reason}`
  when there is no usable data; both render as "n/a" or "no data" rather than
  as numbers. `method.sequential_tuning` is a per-variant map or null.
- `POST {baseURL}/v1/experiments/power` with the baseline mean, variance, daily
  units, arms, alpha, power and CUPED variance reduction, returning the MDE and
  days to reach a target MDE.

Finished results (`status: ok`) are cached in memory for 5 minutes per
experiment and `as_of` day; `insufficient_data` and `error` answers are not
cached, and concurrent requests for the same key share one call. `as_of` is
reduced to a calendar day no later than today. Each call times out after 5
seconds. A slow service shows as a 504, an unreachable or failing one as a 502,
and "no results yet" as a 404, each with a message meant for the person reading
the page. Results and power responses are sent with `Cache-Control: no-cache`,
so the browser always revalidates. The experiments list never calls the service; it summarises whatever
is cached, and hovering a row prefetches that row's results.

Without `analysis.baseURL`, results are generated deterministically from the
registry and labelled `"sample": true`, and the UI badges them as sample data.
The power calculator falls back to a two-sample z-test estimate in Studio.

The decision rule: a significant improvement on the primary metric with every
guardrail passing is `roll_out`; with a guardrail that cannot rule out a drop
beyond its tolerance, `discuss`; with a guardrail significantly hurt,
`do_not_roll_out`. Without a significant improvement it is `keep_running` until
the planned end, then `do_not_roll_out`.

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
| `rollout` | change percentage splits; ramp experiment exposure and set experiment windows |
| `edit_rules` | change targeting queries; start or re-randomize an experiment |
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
| `metadata.experiment` (only through the experiment endpoints) | `metadata.experiment` on every other edit, byte for byte |

Preserved fields surface as read-only in the UI, and the adapter has tests
proving they survive an edit. Within the flag being edited, any part whose
value did not change is written back from the original text, so comments,
alignment and quoting there survive too; if that splice would not read back
identically, Studio falls back to a plain re-render.

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

## Experiment splits

GO Feature Flag's percentages hash `flag name + key` into one bucket space and
lay arms out by variation name, so changing the weights of a test with more
than two arms moves subjects who were already in an arm, and there is no
pass-through. Studio therefore keeps experiments in a separate block that
GO Feature Flag stores but ignores, `metadata.experiment`, keyed by rule name.
Each rule's own `variation` stays what any stock reader serves, and is also
what a subject who is not exposed would get if pass-through is off.

```yaml
request-timeout:
  variations: {control: 200, fast: 150, slow: 250}
  targeting:
    - name: exp-region-a
      query: region in ["region-a"]
      variation: control
  defaultRule: {variation: control}
  metadata:
    experiment:
      version: 1
      hash: md5-shard
      totalShards: 10000
      unit: {type: request, key: targetingKey}   # or {type: entity, key: <attribute>}
      holdout: null                              # or {salt: ..., ranges: [[0, 500]]}
      allocations:
        exp-region-a:
          experimentKey: request-timeout-exp-region-a
          doLog: true
          startAt: 2026-10-01T00:00:00Z
          endAt: null
          passThrough: true
          layer: null                            # or {salt: ..., ranges: [[0, 5000]]}
          splits:
            - variation: control
              shards:
                - {salt: "c1e0a7d25f", ranges: [[0, 100]]}    # exposure: 1%
                - {salt: "9b3f41e2aa", ranges: [[0, 3334]]}   # arm
            - variation: fast
              shards:
                - {salt: "c1e0a7d25f", ranges: [[0, 100]]}
                - {salt: "9b3f41e2aa", ranges: [[3334, 6667]]}
            - variation: slow
              shards:
                - {salt: "c1e0a7d25f", ranges: [[0, 100]]}
                - {salt: "9b3f41e2aa", ranges: [[6667, 10000]]}
```

- **Bucketing.** `shard = uint32(first 4 bytes of MD5(salt + "-" + subject)) mod
  totalShards`. Ranges are half-open. A split matches when every one of its
  shards matches; a shard matches when the value is in any of its ranges. This
  is the salted-shard scheme some existing experimentation platforms use, so
  their salts and ranges can be imported and every subject keeps its arm.
- **Order.** Rules are checked top to bottom. For a rule with an allocation:
  the query must match, then the window, holdout and layer, then the first
  matching split wins. With no split, `passThrough: true` continues to the next
  rule and `false` serves the rule's own variation without logging.
- **Queries** use GO Feature Flag's own query parser. The one difference is that
  `in` lists inside an experiment rule compare values as strings, so a numeric
  attribute `3` matches `["3"]`. The subject key is also available as `id`
  unless the caller sets one.
- **Result.** The evaluator returns the variation, experiment key, allocation
  (the rule name, or `default`), whether to log the exposure, any
  `extraLogging`, and a reason: `SPLIT`, `PASS_THROUGH`, `HOLDOUT`,
  `OUTSIDE_WINDOW`, `STOCK`, `DEFAULT` or `DISABLED`.

Exposure and arm sit on separate salts, which is what makes ramps safe. The
flag page shows an Experiment panel on every rule that has an allocation, and
each change goes through the usual review dialog:

| Change | Permission | What it does |
| --- | --- | --- |
| Ramp exposure | `rollout` | Adds shard values to the exposure salt's ranges. Nobody already exposed moves; exposure cannot go down this way. |
| Start or stop | `rollout` | Sets or clears `startAt` / `endAt`. |
| Start an experiment | `edit_rules` | Lays arms out on two fresh salts from `crypto/rand`, weights apportioned exactly across the shards. |
| Re-randomize | `edit_rules` | New salts, optionally new arms or a lower exposure. Everyone is reassigned, so it needs explicit confirmation. |

Preview uses the same evaluator for any flag with an experiment block.
Services embed it from `pkg/splits`:

```go
exp, err := splits.FromMetadata(internalFlag.GetMetadata())
ev, err := splits.New(splits.FromGOFF("request-timeout", internalFlag), exp)
a := ev.Evaluate(subjectKey, attributes) // a.Variation, a.DoLog, a.ExperimentKey, ...
```

A nine-arm rule evaluates in roughly half a microsecond (Apple M5), and
golden fixtures produced by an existing implementation of the same scheme
agree for all 120,000 subjects in `pkg/splits/testdata/compat`.

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
the rule builder. Charts are small inline SVG components, not a charting
library. Pages other than the flag list are lazy-loaded, and the rule builder's
libraries sit in their own chunk, so the landing page does not pay for them. The built frontend is embedded in the binary with `go:embed`,
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
| `POST` | `/api/environments/{env}/flags/{key}/experiment` | ramp, start, re-randomize or schedule an experiment allocation (`op`: `exposure`, `create`, `rerandomize`, `window`) |
| `POST` | `/api/environments/{env}/flags/{key}/rule` | set a rule's targeting query |
| `POST` | `/api/environments/{env}/flags/{key}/rules` | add a rule |
| `DELETE` | `/api/environments/{env}/flags/{key}/rules/{rule}` | delete a rule |
| `PUT` | `/api/environments/{env}/flags/{key}/rules/order` | reorder rules |
| `POST` | `/api/environments/{env}/flags/{key}/preview` | evaluate against the live engine, or the split evaluator when the flag has an experiment block |
| `POST` | `/api/environments/{env}/flags/{key}/diff` | plain-language description + unified diff, no write |
| `GET` | `/api/environments/{env}/flags/{key}/history` | commits touching this flag |
| `GET` | `/api/environments/{env}/attributes` | attribute names seen in existing rules |
| `POST` | `/api/environments` | create an environment directory |
| `POST` | `/api/environments/{env}/teams` | create a team file |
| `GET` | `/api/experiments` | experiments you can view, with timing and a results summary |
| `POST` | `/api/experiments` | create a registry entry |
| `GET` | `/api/experiments/{key}` | one registry entry |
| `PUT` | `/api/experiments/{key}` | update a registry entry (needs `fileSha`) |
| `POST` | `/api/experiments/{key}/diff` | description + unified diff, no write |
| `GET` | `/api/experiments/{key}/results` | results from the analysis service, or sample results |
| `POST` | `/api/experiments/power` | MDE and duration estimate |
| `GET` | `/api/metrics` | the metric catalog |
| `POST` | `/api/metrics` | add a metric |
| `GET` / `PUT` | `/api/metrics/{key}` | read or update a metric (`PUT` needs `fileSha`) |
| `POST` | `/api/metrics/{key}/diff` | description + unified diff, no write |

Error codes: `403` no permission, `404` unknown flag, `409` stale view or
concurrent edit on the same flag.

## Project status

Working today: the HTTP API, GOFF adapter, OIDC auth, permissions, and the write
path across all three storage backends. The React UI covers the flag list with
search, team filter and inline toggles; flag detail with variations, percentage
sliders and a visual rule builder including negated and nested condition groups;
creating, renaming and deleting flags and teams; editing progressive rollouts and
the schedule (`experimentation`); experiment splits (ramp, start, stop,
re-randomize); review-before-save with a real file diff; live preview; and
per-flag history. The Experiments area covers the registry list, results with
confidence intervals, CUPED, guardrails, segments and a cumulative lift chart,
the set-up form with an MDE calculator, the metric catalog, and a Markdown
readout export.
Light and dark mode, keyboard accessible, protected environments called out and
requiring typed confirmation.

571 tests pass: 383 Go tests plus 14 in the S3 module, 107 frontend tests, and 67
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

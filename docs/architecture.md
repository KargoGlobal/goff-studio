# Architecture: flags and experimentation end to end

This page shows how the pieces fit together: how a flag change reaches the bid path, and how
exposures turn into experiment results. The requirements and priorities behind it are in
[flag_migration_plan.md](../flag_migration_plan.md).

![End-to-end architecture](architecture.svg)

Legend: a solid green box is built and in a draft PR. A solid grey box exists today. A dashed
box is planned and not built yet.

## Flows

Each number matches a marker on the diagram.

1. **Sign in, edit, read.** Flag owners, product managers and MLE use GO Feature Flag Studio in
   the browser. Okta signs them in over OIDC, and the groups claim maps to Studio's
   permission rules. Every permission check is server-side and default-deny.
2. **Reviewed commit.** Every change is shown as a diff first. Studio then commits it to the
   private flags repo as a GitHub App, with the signed-in user as author. A concurrent change
   to the same flag returns a conflict rather than overwriting it.
3. **Publish.** On each push to `main`, a GitHub Action copies the repo to the S3 flags bucket:
   flag files per environment prefix, plus the experiment registry and metric catalog as JSON.
4. **Evaluate.** kraken and scylla pods poll S3 every 60 seconds and evaluate flags in process
   with the GO Feature Flag client and `pkg/splits`. Nothing on the bid path calls Studio or a
   database. `pkg/splits` keeps assignments sticky while an experiment ramps.
5. **Log exposures.** When a subject is exposed to an experiment arm, the service logs an
   exposure event to the Kinesis auction-flow stream. Subjects that pass through a rule are not
   logged; holdout subjects are logged as holdout.
6. **Ingest.** arion's hourly Spark ingest loads exposures into the assignment table (Iceberg,
   mirrored to Snowflake). It dedupes on flag, allocation and subject, so impressions that
   several flags evaluate keep every exposure.
7. **Aggregate.** An hourly chain in the Snowflake `EXP` schema keeps the first exposure per
   subject, joins only active exposures to the auction facts with a 48-hour attribution window,
   rolls up per unit, and writes small summary-statistics tables. Every query is bounded by hour
   watermarks and active experiments.
8. **Analyse.** Archimedes, on Snowpark Container Services inside the Snowflake account, reads
   only the summary tables and the registry. It computes lift and confidence intervals,
   CUPED-adjusted and raw readouts, sequential or fixed-horizon tests, sample-ratio checks,
   guardrails and a roll-out recommendation. A results request takes tens of milliseconds and
   is cached for 5 minutes.
9. **View results.** Studio's backend proxies the results and power APIs with its own cache and
   single-flight requests. The Experiments pages render them, so the browser never talks to
   Archimedes or Snowflake directly.
10. **Notify.** The GitHub Slack app posts flags-repo commits to the change channels, so the
    channel remains the change timeline. Per-team routing comes later from Studio itself.
11. **Explore (planned).** Archimedes will write an `exp_results` table to Snowflake for an Omni
    Experiments topic and template dashboard. Analysts can already ask for results through the
    Archimedes MCP tool.

## Where state lives

| What | Where | Notes |
| --- | --- | --- |
| Flags and split configuration | YAML in the private flags repo | Git is the source of truth; every change is a reviewed commit. |
| What services read | S3 copy, held in memory per pod | Refreshed every 60 seconds; no database on the bid path. |
| Experiment registry and metric catalog | `experiments/*.yaml`, `metrics/*.yaml` in the flags repo | Loaded hourly into `EXP_REGISTRY` and `EXP_METRICS`. |
| Exposures, outcomes, summary statistics | Snowflake `EXP` schema | The only database in the design. |
| Experiment results | Computed on request by Archimedes | In-memory caches in Archimedes and Studio; `exp_results` table planned for Omni. |
| Studio sessions | Encrypted browser cookies | Studio has no database. |

## Components and status

| Component | Status | Where |
| --- | --- | --- |
| Studio: flags, experiment splits, Experiments and Metrics pages | Built, draft PR | goff-studio #3 |
| `pkg/splits` sticky split evaluator | Built, draft PR | goff-studio #3 |
| Experiment analysis engine and results API | Built, draft PR, reviewed separately | archimedes #13 |
| Assignment dedup fix | Built, draft PR | arion #5487 |
| `EXP` schema and hourly aggregates | Built, draft PR; grants still needed | arion #5488 |
| Private flags repo, GitHub Action, S3 bucket | Planned | not started |
| kraken and scylla adapters | Planned | not started |
| `exp_results` table and Omni topic | Planned | not started |

## Before production

- **Grants on the `EXP` schema** for the ETL, read-only and Archimedes roles. They are
  listed in arion #5488 for the data platform team.
- **Real service authentication for Archimedes.** Its routes trust caller-supplied identity
  until the planned Keycloak integration lands.
- **A Snowflake dry run** of the `EXP` SQL. It was checked in DuckDB, which verifies the logic
  but not Snowflake-specific syntax.
- **The service adapters and the flags repo publishing path** (flows 3 to 5).

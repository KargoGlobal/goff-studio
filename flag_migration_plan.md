# Flag and experimentation migration: requirements assessment

Status: draft, 2026-09-25. Owner: platform and MLE teams.

This document assesses what our feature-flag and experimentation usage requires of its
next platform, and plans the move to GO Feature Flag, managed through GO Feature Flag Studio
(this repo). It starts from how flags, tests, analysis and reporting are used today (section 1)
and derives the requirements for each area:

- how traffic is split for tests (section 3)
- how experiments are analysed, including CUPED and stratification (section 4)
- how results are reported in Studio and the BI tool (section 5)
- how the MLE team tests models (section 6)

The end-to-end architecture diagram is in [docs/architecture.md](docs/architecture.md).

Features are listed by track and priority in section 7. The guiding rule is **the simplest
migration that keeps every service behaving the same on day one**, including in-progress
ramps. Improvements come after cutover.

---

## 1. What we run today

### Inventory

- **73 active flags**: 29 boolean, 22 string, 17 JSON, 4 numeric, 1 integer.
- **Two environments**: Staging and Production. In the flags sampled, Staging holds only a default rule or one rule, and the real targeting lives in Production.
- **39 flags have not been edited in over a year.** Some are still read by code (for example
  `ctr_model`), so "untouched" does not mean "unused". The import keeps them all and
  the stale-flag report (F20) sorts them out afterwards.
- **Seven SDK keys, one per service per environment**: the bidder, the exchange (two in production) and
  the video player. None is scoped to a subset of flags.
- **No saved audiences, no predefined targeting attributes and no mutual-exclusion layers.**
  Every rule is written inline on its flag.
- **Two tags** (supply shaping, OpenRTB testing) cover 9 flags. Starring and archiving are also used.

### Services that evaluate flags

| Service | Language | Subject (bucketing) key | Value types | Assignment logging |
| --- | --- | --- | --- | --- |
| **bidder** | Go | lineItemID, adSlotID, bidID or dealID, chosen per flag | bool, numeric, string, JSON | none (logger is a no-op) |
| **exchange** | Go | impressionID, sometimes request ID | bool, numeric, integer, string, JSON | every assignment, as experiment events to the warehouse |
| **video player loader** | browser JS | random per page load | JSON | vendor-side |

Both Go services already wrap the SDK behind a local `Experimenter` interface
(`internal/experiment/experiment.go` in each repo), exposing
`GetBool / GetFloat64 / GetString / GetJSON` (plus `GetInt64` in the exchange) that fall back to a
default value on error. **That interface is the migration seam.** Only the
implementation behind it changes. Call sites don't.

Targeting attributes in use: publisherID, lineItemID, lineItemType, campaignID, dealID,
adSlotID/Type/Size, adFormatName, mediaType, inventoryType, auctionType, countryCode,
domain, deviceType, dspID, serverRegion/region, isVBX, containsBuyerUID, podName,
clusterDomain and a few others. Every one is a plain string, number or boolean. The
GO Feature Flag query language (`eq`, `in`, `co`, `sw`, `gt`, and so on) covers all of them.

### Rule patterns the new setup must express

Three patterns cover almost every flag:

1. **Kill switch or single value.** The flag is on or off with one default value.
   Most boolean flags work this way.
2. **Per-entity overrides.** One rule per ID, each serving its own variation, then a default.
   For example, `bidding-strategy` has 14 production rules of the form "lineItemID is one of X,
   serve X's JSON config", more than 60 variations, and the default "No Bidding Strategy".
   GO Feature Flag handles this directly (one targeting rule per ID), but the UI needs to make
   long rule lists manageable (F16).
3. **Partial exposure with pass-through.** A rule matches a segment (e.g. `serverRegion is one
   of us-east-1`), sends 1% of it to nine test arms split evenly, and lets the other 99% **pass
   through** to the next rule. `tmax` runs four such regional rules, each linked to an
   experiment analysis. In GO Feature Flag the first matching rule decides, and there is no
   pass-through. The split evaluator in section 3 covers this.

### How flags are used

- **Mostly configuration and kill switches, some A/B.** Most bidder flags are rollout gates or
  JSON config (bidding strategy, frequency caps, supply shaping, OpenRTB field overrides).
- **ML model routing.** The bidder's current model-routing loader reads a JSON flag of the form
  `{name, versions:[{name, uri, weight}]}` every minute and picks a model version by weight.
- **Experiments with warehouse analysis, now dormant.** No analysis has started since
  February 2026. The exchange's assignments still land in
  the warehouse assignment table, and a pipeline job joins them to outcomes every 3 hours in
  the response table. Section 4 describes it and its replacement.
  Rules that feed a running analysis carry a warning that edits could affect the analysis.
- **Per-flag env-var master switches** exist in the exchange and the bidder to limit metered
  SDK calls. Self-hosted evaluation has no per-call cost, so these become optional.

### Access and permissions today

- **Sign-in is Okta SSO.** Users are invited individually, then routed through Okta.
- **83 users across four roles:**

  | Role | Users | What it allows (the platform's standard role definitions) |
  | --- | --- | --- |
  | Viewer | 22 | read only |
  | Experiment Editor | 38 | edit flags and experiments |
  | Data Owner | 12 | editor rights plus metric and warehouse definitions |
  | Admin | 11 | everything, including users, keys and integrations |

- **Teams exist but don't restrict editing.** A bidder team (about 37 members), a video player team,
  an ML team and a few small ones. Team only labels ownership, and most JSON flags have no team
  at all. Any editor can change any flag.
- **Production approvals are available but switched off.**
- **Change history is versioned per flag.** Each entry has a plain-language summary, user,
  environment, a details diff and one-click restore to that version. `tmax` alone has 57 versions.

### Slack today

- **A workspace-level Slack app posts to two channels**, a flag-notifications channel and
  a deployments channel. For example: "Feature Flag *tmax* was turned *OFF* by *&lt;user&gt;*."
- **Each channel has checkboxes for which events it receives.** The flag events are
  "flag enabled or disabled in production" and "rule added, edited or deleted in production".
  The other events are experiment status, data-quality and significance alerts.
- **Staging changes are not announced.**
- **Per-flag channel subscriptions exist but no flag uses them.** So every team sees every
  production change.
- **Email notifications and the outbound webhook are both unconfigured.**
- **The channel serves as a timeline.** People reply in the notification thread to mark
  changes as intentional, and incident reviews use the channel as the change record.

### Deadline

The current platform must be fully exited by **December 31, 2026**. The rollout in section 8
fits inside that window with about two weeks of slack.

### Lessons from two production incidents (2025-11 and 2026-08)

Both were flag misconfigurations that reached full production traffic:

- A variation value that effectively discarded all traffic was ramped widely.
- A split with no targeting condition matched all traffic when it was meant to match one line item.

The features below prioritise guardrails against these two failure modes.

---

## 2. Target architecture

```
  Okta (OIDC + groups claim)
          │
   GO Feature Flag Studio ── Flags · Experiments · Models ──conditional write, author metadata──►  S3 flags bucket (versioned)
          ▲            │                                                                  staging/*.goff.yaml
          │            └──► Slack (per-team change notifications)                         production/*.goff.yaml
          │ reads results                                                                 experiments/*.yaml   metrics/*.yaml
          │                                                                                        │ 60s poll
          │                                                     ┌──────────────────────────────┴──────────────┐
          │                                                   the bidder                                      the exchange
          │                                     (shared flags-sdk package: GOFF client + split evaluator)
          │                                                     │ exposure events (only exposed subjects)
          │                                                     ▼
          │                                  the event stream ─► the data pipeline ─► the warehouse: exp_exposure ─► exp_unit_agg ─► exp_suffstats
          │                                                                                                   │
          │                                             the analysis service experiment_analysis (next to the warehouse)
          └──────────────── results API (cached) ◄──────────────────────────────────────────────────── exp_results
                                                                                                          │
                                                                                          BI "Experiments" model + template
```

Decisions, and why each is the simplest option:

- **Studio writes straight to a versioned S3 bucket.** There is no Git repository and no sync
  job in between. Studio's S3 backend writes with conditional requests, so a concurrent change
  still returns a conflict instead of overwriting. Bucket versioning keeps every version for
  history and restore. Each version carries the author and a plain-language summary in object
  metadata, so history keeps attribution. Only Studio's IAM role can write to the bucket, which
  plays the part branch protection would play in Git. Experiment definitions and the metric
  catalog live in the same bucket, so they get the same history.
- **What this gives up, and the mitigation.** There is no pull-request review or CODEOWNERS.
  Studio's server-side permissions, the diff-before-save dialog and typed confirmation on
  protected environments are the review. Git blame is replaced by the version history in
  Studio. Change notifications come from Studio itself rather than a Git integration.
- **One shared Go package evaluates flags everywhere.** The bidder, the exchange and Studio's preview
  panel all import the same `flags-sdk` package, so a subject gets the same answer in every
  place. It wraps the stock GO Feature Flag client and adds the split evaluator (section 3).
  It is in-process, with no network hop on the bid path and no new service to run.
- **Stock GO Feature Flag handles everything except experiment splits.** Kill switches,
  per-entity overrides, JSON config, file format, S3 retrieval and caching all stay stock.
  Only flags that carry an experiment block use the split evaluator.
- **Services read the same bucket.** Pods poll it every 60 seconds with credentials they
  already have through IRSA. S3 handles that polling volume without the rate limits a Git host
  would impose.
- **No SDK keys.** Read access is the service's IAM role on the bucket prefix, scoped per
  environment. That replaces today's seven per-service keys with nothing new to rotate.
- **No database for flags.** The files are the source of truth.
- **Analysis runs in the warehouse on small pre-aggregated tables.** Raw exposure and outcome rows
  are reduced once, incrementally, to per-unit and then per-variant sufficient statistics.
  Every statistic is computed from those thousands of rows, never from the raw tables.
- **CUPED and the rest of the statistics run in the analysis service.** The analysis service is the company's governed
  analytics service, running in the warehouse's container runtime. It already runs deterministic,
  policy-checked statistical analyses, including experiment design and geo experiments, and it
  exposes them over an API and MCP. Its runtime sits inside the warehouse account next to the
  summary tables. It reads only those tables, so a results request computes in milliseconds.
  Studio's backend calls its results API with a service identity and caches the response.
  Putting the kernel there, rather than in a stored procedure or in Studio, keeps one audited
  implementation that Studio, the BI tool write-backs and analysts asking through MCP all share.
- **The statistics are paper-backed and cross-checked.** The engine is written with numpy,
  scipy and statsmodels, with each method citing its source. GrowthBook's `gbstats` (MIT)
  could not be adopted as the kernel because its pinned dependencies conflict with the
  service's, but it runs as an optional cross-check. Sequential intervals and ratio standard
  errors match it to 1e-9.
- **Studio is the operational surface and the BI tool is the exploration surface.** Studio shows
  set-up, live monitoring and the decision view for one experiment. The BI tool holds the semantic
  model, the standard dashboard template and cross-experiment reporting.

---

## 3. Split testing: how traffic is divided

### Why stock percentages are not enough

GO Feature Flag hashes `flag name + key` once per flag and lays arms out in one shared bucket
space, ordered by variation name. That is fine for on/off rollouts but breaks multi-arm tests.
A simulation against the GO Feature Flag core module gave these results:

| Change | Subjects who switch arm |
| --- | --- |
| 2 arms, 90/10 to 80/20 | only the 10% new entrants. Sticky. |
| 3 arms, 90/5/5 to 80/10/10 | 15%, including **half of the subjects already in a test arm** |
| 9 arms plus pass-through, exposure 1% to 2% | **88.7% of already-exposed subjects** |

It also has no pass-through, no salt (renaming a flag reshuffles everyone), no layers and
no holdouts.

### The split model

The shared evaluator uses a salted shard model. It is the same algorithm the current platform
uses, so importing today's salts and ranges keeps every subject in the arm it is in now.

- **Bucket.** `shard = uint32(first 4 bytes of MD5(salt + "-" + subjectKey)) mod 10000`.
  Precision is 0.01%, and the 0.11% arms in use today are expressible.
- **Allocation.** Each targeting rule can carry an allocation with one or more splits. A split
  matches when all of its shards match, and a shard matches when the bucket falls in any of its
  half-open ranges.
- **Exposure and arm are separate salts.** One shard decides whether the subject is exposed at
  all, a second decides the arm. Raising exposure from 1% to 2% only adds new subjects, and
  nobody already exposed changes arm.
- **Pass-through.** A subject that matches a rule's query but not its exposure shard continues
  to the next rule. Nothing is served from that rule and nothing is logged.
- **Re-randomization.** A new salt reshuffles one experiment on purpose. Renaming a flag changes
  nothing.
- **Mutual-exclusion layers.** Experiments that must not overlap share a layer salt and own
  disjoint ranges of it. Studio checks for overlap before saving.
- **Holdouts.** A global shard checked before any experiment rule. Subjects in it get the
  default and are logged as holdout.
- **Randomization unit.** Each experiment declares `unit: request` (impression, bid or request
  IDs) or `unit: entity` (ad slot, line item, deal) and the attribute that carries it. Stickiness
  and ramp safety only mean something for entity units, and Studio says so.
- **Stratified assignment for entity units.** For line item or deal tests, Studio can generate a
  block-randomized assignment. It groups units into strata (for example vertical × media type
  × pre-period spend decile), ranks by `hash(unit + salt)` within each stratum, and alternates
  arms. The result is written as an explicit unit list per arm, which is reviewable in the diff.
- **Exposure-only logging.** The evaluator returns the variation, the allocation key and a
  log flag. Only exposed subjects are logged, through the existing logging path.
- **Type handling.** The current platform compares list-membership values as strings. The
  evaluator does the same for `in` checks inside experiment rules, so numeric attributes such
  as device type keep matching.

### Flag format

GO Feature Flag drops unknown fields on rules, so the experiment block lives in flag
`metadata`, keyed by rule name. Each rule's stock `variation` is what any stock reader sees and
is also the pass-through value.

```yaml
tmax:
  variations: {control: 200, tmax150: 150, tmax225: 225, tmax250: 250}   # ...
  targeting:
    - name: exp-us-east-1
      query: serverRegion in ["us-east-1"]
      variation: control              # stock fallback == pass-through value
  defaultRule: {variation: control}
  trackEvents: false
  metadata:
    team: bidder
    experiment:
      version: 1
      hash: md5-shard
      totalShards: 10000
      unit: {type: request, key: targetingKey}
      holdout: null
      allocations:
        exp-us-east-1:                # == rule name; imported allocation key kept
          experimentKey: tmax-exp-us-east-1
          doLog: true
          startAt: 2026-10-01T00:00:00Z
          endAt: null
          passThrough: true
          layer: null
          splits:
            - variation: tmax150
              shards:
                - {salt: "<imported exposure salt>", ranges: [[0, 100]]}
                - {salt: "<imported arm salt>",      ranges: [[0, 1111]]}
            # one entry per arm
```

---

## 4. Analysis and statistics

### What happens today

**Usage.**
- **98 analyses exist, and none is running.** The last one started in February 2026.
  Activity peaked from February to July 2025.
- **Test families:** EID provider and transform tests, buyer-UID tests, the tmax versions and
  regions, PMP deal ordering and deal-size limits, DSP response-rate tests, native and
  banner/video field overrides, and video-player tests (Prebid versions, ad units, VPAID).
- **No model (CTR, VCR, VSR) or CTV attention test has ever been set up as a formal analysis.**
  Those are judged in the BI tool and Grafana.
- **Two entity types:** `bid_id` for exchange and bidder tests, and `video_player` for the
  video-player tests. The video-player tests read video player impression tables.
- **Diagnostics:** 58 passed, 24 failed, 6 warnings, 10 not run.

**Configured method.** Every analysis checked uses the company defaults:

| Setting | Value |
| --- | --- |
| Test type | Sequential confidence intervals |
| Confidence level | 95% |
| CUPED | off |
| Multiple-testing correction | off |
| Winsorization | off on bidder and exchange metrics |
| Stratification | none |
| Precision target | ±5% |
| Refresh | one batch a day at 3 AM ET |

Non-inferiority guardrail cutoffs exist but are unused.

**Metric catalog.** 21 metrics and 20 facts from four fact queries:
- **Bidder and exchange metrics**, all per-bid sums or 0/1 rates:
  - DSP bid rate: used in 83 analyses.
  - DSP win rate, bidder win rate, bidder served rate.
  - Total gross revenue, net CPM, PMP gross revenue, publisher net revenue.
  - tmax exceeded, click rate, video complete rate.
  - Average bid CPM: the only ratio metric.
- **Video-player metrics:** RPM, eCPM, fill rate, header-bidding rates, impressions, page
  views, requests.
- **Primary metric:** DSP bid rate in 75 of 98 analyses.

**What people look at.**
- **The metrics table:** lift with CI per arm, for multi-arm tests with up to 9 arms.
- **The traffic tab:** expected vs actual split.
- **A 9-check diagnostics list.**
- **Breakdowns by DSP name.**
- **A rule-based recommendation:**
  - Significant positive with no guardrail problem: roll out.
  - Significant positive with a guardrail risk: discuss.
  - Significant positive with a guardrail significantly negative: don't roll out.
  - Neutral or negative: don't roll out.
- The hypothesis and key-takeaway fields are empty everywhere.

**Operational problems in the current setup.**
- **The shared assignment query is hand-edited for each test.** It is currently hard-coded to
  one experiment, which breaks re-runs of the other 84 analyses on that source.
- **The PMP revenue fact has an operator-precedence bug.** `Preferred OR Private AND <date
  window>` has no parentheses, so the date filter applies only to Private auctions.
- **Automatic end dates are off.** A March 2025 video-player analysis was still recomputing
  daily in September 2026, at 2.5 to 15.6 hours per run.
- **Automatic clean-up of old warehouse tables is off.**
- **The metrics are not defined in git.**
- **One metric source's incremental aggregate hits the 4-hour timeout.** Warehouse query logs show it timing out daily from September 21 to 24, on a Medium warehouse,
  about 16 credits per failed attempt. The
  platform's SQL full-scans about 10 TB of response data for 30 days, and data engineering has
  asked for incremental processing without result.
- **The response table is at impression × DSP-response grain** with no experiment or variant
  columns, so every metric query re-joins about a trillion assignment rows on a binary ID.
- **Outcomes join only within a 4-hour window,** so late clicks, completes and viewability are
  missed unless the hour is re-triggered.
- **The assignment load drops exposures.** It dedupes on the subject only, both within a batch
  and against the last 12 hours (the assignment insert in the data pipeline's auction ingest, current `master`).
  When two flags evaluate the same impression, only one flag's exposure survives. That can
  cause sample-ratio mismatch and bias on its own, and it should be fixed now, whatever
  else happens.
- **The bidder logs no assignments at all,** and its model predictor events carry no bid ID, so
  bidder model tests cannot be joined to outcomes through this pipeline.
- **In practice decisions are made by ramping and watching.** A product manager posts the
  flag, a BI dashboard and a Grafana link, and ramps 1% → 5% → … → 100% over days or weeks.
  The formal reports are exported as PDFs or screenshots, and the video team does its analysis
  directly in the warehouse.
- **Sample-ratio mismatch comes up repeatedly.** At hundreds of millions of units even tiny
  imbalances are flagged, and one 15/85 split was observed as 82/18.
- **Stratification has been asked for since 2023.** Data science has repeatedly said that the
  lack of stratified sampling limits model-version testing.

### Data model

All tables live in a new `exp` schema and are built by the data pipeline.

| Table | Grain and key | Purpose |
| --- | --- | --- |
| `exp_registry` | experiment | Loaded from `experiments/*.yaml`. Only registered experiments are processed. |
| `exp_metrics` | metric | Loaded from `metrics/*.yaml`: numerator, denominator, source columns, cap, direction. |
| `exp_exposure` | (flag, allocation, subject), first exposure kept | Replaces the assignment table for analysis. Stores first exposure time, variation, stratum, typed attributes and the bid ID for joins. Clustered by (flag, first exposure hour). Loaded with an hourly `MERGE … WHEN NOT MATCHED`. |
| `exp_outcome_hourly` | (bid or impression ID, hour) | Narrow outcome rows (bid, win, served, click, video complete, viewable, revenue), semi-joined to active exposures only. That prunes about 99% of the outcome facts. |
| `exp_unit_agg` | (experiment, variant, stratum, unit) | Per-unit sums over a 48-hour attribution window, plus the CUPED or CUPAC covariate. Re-merged for the last N days when late upstream data lands. |
| `exp_suffstats` | (experiment, variant, stratum, metric, day) | n, ΣY, ΣY², ΣD, ΣD², ΣYD, ΣX, ΣX², ΣXY and the ratio-covariate cross terms. Thousands of rows. |
| `exp_results` | (experiment, metric, variant, as-of time) | Lift, CI, p-value, adjusted and raw, SRM and guardrail status. Read by Studio and the BI tool. |
| `flag_change_log` | change | Who changed which flag, when, before and after. Built from the bucket's version history and its author metadata. Used for chart annotations and incident timelines. |

Rules that keep this correct:

- **Aggregate to the randomization unit first.** The response data has many DSP rows per
  impression, and treating them as independent understates variance.
- **Squares only add up across days when each unit lives in one day.** That holds for
  per-impression units. For ad slots, line items and deals, keep a cumulative per-unit table and
  recompute the sums from it.
- **Winsorize revenue and bid price at the pooled p99.9,** frozen from the pre-period or the
  first day, never per arm.
- **For very large per-impression tests, a deterministic 5% hash sample of exposures** costs
  only a 1/0.05 variance factor and removes the heavy join entirely.

### Methodology

Notation: arm k, unit i, outcome Y, ratio denominator D, pre-treatment covariate X.

- **Means.** Per-arm mean and variance from the sums. At these volumes use z, not t, except
  for entity tests with fewer than about 50 units per arm.
- **Ratio metrics** (CTR, VCR, VSR, win rate, revenue per bid). R = Ȳ/D̄, with the delta-method
  variance Var(R) ≈ (1/n)[s²_Y/D̄² − 2Ȳ·s_YD/D̄³ + Ȳ²·s²_D/D̄⁴]. Relative lift
  L = R_T/R_C − 1, with Var(L) ≈ Var(R_T)/R_C² + R_T²·Var(R_C)/R_C⁴.
- **Clustered designs.** When the analysis is per impression but randomization is per
  line item or deal, the metric is a ratio of per-cluster sums with n = number of clusters.
  The delta method handles it without estimating intra-cluster correlation.
- **CUPED.** θ = Cov(Y, X)/Var(X) on pooled arms, per metric. Adjusted mean
  Ȳₖ − θ(X̄ₖ − X̄). Variance ≈ s²_Y(1 − ρ²)/nₖ. Units with no pre-period get a
  two-covariate version (X·1{pre}, 1{pre}), never zero-imputation. Ratio metrics use the
  regression-adjusted ratio form, which `gbstats` implements.
- **CUPAC for per-impression tests.** An impression has no pre-period, so the covariate is a
  prediction from request-time features. Examples: trailing 7-day bid, win or VCR rate for the
  ad slot × DSP × device key, or an existing model score. It must not be affected by treatment.
- **Post-stratification.** Δ = Σ wₕ(Ȳ_T,h − Ȳ_C,h) with wₕ = Nₕ/N and
  Var = Σ wₕ²(s²_T,h/n_T,h + s²_C,h/n_C,h). It combines with CUPED as a regression with stratum
  fixed effects plus the covariate.
- **Fixed horizon or sequential.** The default is a fixed horizon in whole weeks, sized by the
  MDE calculator, so day-of-week effects are covered. A "safe to peek" toggle switches to
  always-valid confidence sequences (mSPRT form). Small line item tests can use group sequential
  boundaries with O'Brien-Fleming spending.
- **Multiple arms and metrics.** Holm–Bonferroni (or Dunnett) across arms on the primary metric.
  Benjamini–Hochberg on secondary metrics and segment breakdowns. The MDE calculator sizes at
  α/(K − 1) for K arms.
- **Sample-ratio check.** χ² on distinct exposed units, never response rows. Alarm at
  p < 0.001, overall, per day and per stratum. The UI also shows the absolute deviation
  |Oₖ/N − πₖ|, so a significant but negligible imbalance at 10⁸ units can be triaged.
- **Guardrails.** One-sided non-inferiority: the metric passes when the lower CI bound of Δ is
  above −δ. Default guardrails are gross revenue, margin, tmax-exceeded rate, bid rate and
  latency.
- **Power and MDE.** MDE = (z₁₋α/2 + z₁₋β)·√[σ²(1 − ρ²)(1/n_T + 1/n_C)], with σ² from history
  per metric and unit, and the design effect for clustered tests.
- **Interference.** When arms share line-item budgets or pacing, per-impression randomization
  leaks between arms. Pacing and bid-shading tests should randomize by line item, and Studio's
  set-up screen warns about it.

### Compute

1. An hourly warehouse task merges the new hour into `exp_exposure`, `exp_outcome_hourly` and
   `exp_unit_agg`, then rebuilds `exp_suffstats`. The watermark is the hour column, which is
   the clustering key.
2. The analysis service's `experiment_analysis` runs the statistics kernel over `exp_suffstats` on request
   and on a schedule. It serves results to Studio through `GET /v1/experiments/{key}/results`,
   caches them for five minutes, and writes `exp_results` for the BI tool.
3. An XS or S warehouse is enough, because nothing scans raw facts after the first reduction.

---

## 5. Reporting and BI

### What exists today

- **Experiment reporting is spread across hand-built BI dashboards** in a tests folder and a few others. Examples: the VSR model test, CTV attention tests, programmatic VCR
  tests, direct CTR version comparisons, bidding-strategy line tests, pricing-model diagnostics.
  Four of them contain near-identical "control vs test" tiles with hard-coded deal or line filters.
- **No BI model covers experiment assignments.** Every dashboard rebuilds the split from
  auction facts.
- **No dashboard shows confidence intervals or a sample-ratio check.** They show daily
  side-by-side lines, and mix shift confounds the comparison.
- **There is no model-version split or predicted-vs-realized view.** The VSR incident
  post-mortem found an 11-hour detection lag because only blended margin was watched. The
  follow-up tickets ask for margin and win rate by model version × auction type × placement
  and predicted-vs-actual monitoring.
- **Bidder auction data arrives in the BI tool about two days late,** so early ramps rely on Grafana.
- **Incidents are reconstructed from flag history** by lining up change times with metric reversals.

### Where each view lives

| Surface | What goes there |
| --- | --- |
| **Studio** | Experiment set-up, live rollout monitor, the results and decision view for one experiment (with the diagnostics checklist and roll-out rule), readout export. Operational and per-experiment. |
| **BI tool** | The semantic model over the `exp` tables, the standard experiment dashboard, the portfolio view, ad-hoc slicing, history. |
| **Grafana and data observability** | Hourly and real-time monitoring and alerting. BI data lags, so alerts never depend on it. |

### Studio views

1. **Experiments list.** Every live experiment with owner, flag, exposure, days running,
   sample-ratio status, guardrail status and decision status.
2. **Set-up.** Hypothesis, owner and ticket. Randomization unit and analysis unit, with an
   automatic clustering note. Arms and expected split. Strata with a balance preview. Primary,
   secondary and guardrail metrics from the catalog. CUPED or CUPAC toggle with the covariate and
   the expected variance reduction from history. Fixed or sequential, α and power. The MDE and
   duration calculator. An optional A/A arm, which teams already use to build trust.
3. **Rollout monitor.** Current allocation per arm and the ramp timeline annotated from the
   flag change log. Near-real-time exposure counts from Prometheus. Guardrail status and a kill
   switch. Links to the matching Grafana panels.
4. **Results.** Variant names, never letters. Per metric: lift with CI, raw and CUPED-adjusted
   side by side, with the variance reduction shown. p-value or always-valid band, sample size per
   arm. A blocking sample-ratio banner with per-stratum drill-down. Guardrail pass or fail.
   Cumulative lift chart. Segment breakdowns by auction type, media type, device, DSP and ad slot
   with adjusted q-values. For model flags, predicted vs realized on the same grain. A data
   freshness watermark.
5. **Readout export.** A decision block with hypothesis, primary and guardrail results and
   ship, extend or kill, exported as Markdown for Confluence, with a stable link to the BI tool
   dashboard.

### BI tool

- **An "Experiments" topic** over `exp_results`, `exp_suffstats`, `flag_change_log`,
  `exp_registry` and a view of exposures joined to model predictor events. Standard measures:
  VSR, VCR, CTR, win rate, response rate, margin %, net revenue, gross CPM, price ratio,
  negative-margin bid share, revenue per 1,000 requests or bids, and lift vs control.
- **One verified experiment dashboard template,** parameterized by experiment, replacing the
  per-test copies. Tiles:
  1. KPI scorecard by variant with lift and CI.
  2. Daily control vs test per metric.
  3. Lift and CI by auction type and model version.
  4. Margin delta.
  5. Top DSPs and publishers by variant.
  6. Ad-slot or deal pacing.
  7. Predicted vs realized by auction type, for model tests.
  8. Flag-change annotations.
- **Every time-series tile also shows a 3-month rolling baseline and year-over-year.** Teams
  already fall back on year-over-year trends when a clean split is unavailable.
- **A portfolio view** listing live experiments, owners, exposure, days running and decision
  status, rolled up the way the existing business-impact dashboard already reports lift.

---

## 6. Model testing for the MLE team

### Today

| Step | Owner | How | Evidence of time |
| --- | --- | --- | --- |
| Train | Data science | Model repo, SageMaker pipeline, artifacts in S3, runs in MLflow | days to weeks |
| Register | Data science, product | Two hand-kept Confluence tables. No real registry. | manual |
| Nightly retrain | automatic | A `latest.conf` pointer flips each night and the ML client picks it up within 30 seconds. No approval. | daily; the VSR incident came from this |
| New version into the ML client | MLE engineer | Model code in the ML client library, then an ML client library release, subject to code freezes | CTR model v104: pipeline ready 7/10, the ML client library release 9/24 |
| Deploy to the bidder | platform engineer | Bump the ML client library, deploy. The routing policy only loads at startup, so pods must roll. | v104: about 11 weeks end to end, still at 0% |
| Monitors | MLE engineer | Grafana and data observability alerts built by hand per release | about a sprint |
| Route traffic | platform engineer only | Flag gates keyed on ad slot, plus the ML client's allocation percentage, plus the JSON weights flag | hours to days per step; "about 10 minutes to propagate" |
| Ramp | product manager | 1% → 5% → 15% → 30% → 50% → 75% | 2 to 10 weeks |
| Compare | data science, analytics | Per-test BI and Grafana dashboards, offline notebooks | days; the BI tool lags 2 days |

Problems, most important first:

1. **A new model version needs a release in two repos and waits on freezes.**
2. **MLE and data science cannot change routing themselves.** Every step is a Slack request.
3. **Routing cannot express proper comparisons.** There are three routing layers with
   inconsistent randomization. The JSON weights loader and the ML client both pick a version
   at random on every request, while the flag gates hash on ad slot. Product asked for tests by
   line item instead of a percentage ramp.
4. **Results cannot be attributed to a model version.** Flag exposures are never logged in
   the bidder, the predictor event is 1% sampled with no bid ID, and some paths hardcode the model
   label instead of the version.
5. **There is no per-version calibration monitoring.** In the VSR incident, predicted 0.98 vs
   actual 0.72 was found about 11 hours late through margin dashboards.
6. **Nightly auto-promotion is an unreviewed production change every day.**
7. **Propagation is slow and unclear.** Two 60-second polls stack, and some changes need pods to roll.

The ML client already has a way to pin a version per request and skip its random picker.
The bidder never calls it.

### Target

- **A model-routing flag kind.** Variations are `{family, version, mode}` picked from a model
  catalog (S3 artifacts, MLflow runs, the ML client library model families). Studio checks that the
  artifact exists and that the version is loaded in each region before allowing the save.
- **Sticky routing by default.** Routing goes through the split evaluator, keyed on line item by
  default so pacing stays consistent, with ad slot, deal or bid as alternatives. Per-request
  random is an explicit option only.
- **No pod restarts.** The bidder preloads every version in the policy and pins the chosen one per
  request. A routing change is live within the poll interval.
- **Exposure logging on every bid.** Flag, variation, model version and bid ID are logged on
  every bid. Only the inputs payload is sampled. Every path sets the version label.
- **Automatic per-arm metrics.** Model quality: predicted vs realized CTR, VCR, VSR and win rate,
  calibration by decile and segment, log loss and AUC. Business: win rate, margin, net revenue,
  spend and pacing, and the share of bids clamped at floor or ceiling. These feed the same
  results tables, and Prometheus metrics carry the variation label for real-time guardrails.
- **Guardrails with alerting, then auto-rollback.** Thresholds such as a calibration-ratio limit
  or a margin drop versus control page the owner first. Once trusted, a breach restores the
  previous routing version automatically.
- **MLE-scoped permissions.** MLE and data science edit only the model-routing file, self-serve
  up to a ramp cap (for example 10%). Above the cap needs a second approver from platform or
  product. Freeze windows are enforced, and every change is announced in the team channel.
- **Shadow mode.** Score with the challenger alongside the champion, log both, serve the champion.
- **A promotion gate for nightly retrains.** Moving the `latest.conf` pointer becomes an
  approved Studio action with a stored per-segment backtest.

Constraint to keep: the current JSON weights loader re-reads its flag with a random subject
every minute. If that flag were ever given a split, the whole config would flap every minute.
Keep it single-variation until it is replaced by the model-routing flag kind.

```yaml
model-routing.directctr:
  metadata:
    kind: model-routing
    team: mle
    policy: directctr
    guardrails: {calibration_ratio_max: 1.25, margin_drop_pct: 5, min_exposures: 50000,
                 self_serve_max_pct: 10, action: alert}
    experiment:
      unit: {type: entity, key: lineItemId}
      # allocations as in section 3
  variations:
    champion:    {mode: serve,  family: directctr, version: v102, source: legacy}
    v104:        {mode: serve,  family: directctr, version: v104}
    v104-shadow: {mode: shadow, family: directctr, version: v102, source: legacy,
                  shadow: [{version: v104, sample: 0.1}]}
  targeting:
    - name: ctv-test-lines
      query: auctionType eq "direct" and lineItemId in ["1001", "1002"]
      variation: v104
  defaultRule: {variation: champion}
```

Bidder changes, by file:

- `internal/experiment/experiment.go`: add `GetModelRoute(ctx, policy, subject, attrs)` backed
  by the shared package, with exposure events instead of the no-op logger.
- `bidder/filters.go`, `bidder/bidreduction.go`, `bidder/optimized_bid.go`: replace the boolean
  gates with route resolution and pin the version on the predict call. A `legacy` source keeps
  the older model paths as named arms.
- `internal/predictor/predictor.go` and the ML client library predictor: preload every version in the
  policy and hot-reload the policy. Retire the JSON weights loader and its deployment settings.
- `event/predictor.go`: add bid ID, flag, variation, mode and model version, and set the
  version everywhere.
- `internal/metrics/vsr.go` and the threshold metrics: add the variation label.

---

## 7. Features by priority

Sizes: S is under 2 days, M is under a week, L is over a week. "Service" means work in the bidder or
the exchange, "Data" means the data pipeline and the warehouse, "Studio" means this repo, "Config" needs no code.

Summary of what must be live before **December 31, 2026**, the P0 items:

- **Flags:** F1 to F7. Evaluation behind the existing interface, import, Okta, permissions,
  Slack, deploy, catch-all guardrail.
- **Split testing:** X1 and X2. The shared evaluator and an exact import of today's buckets.
- **Analysis:** A1 to A8. Exposure logging, the dedup fix, the aggregate tables, the stats job,
  the experiment registry and metric catalog, and validation against current results.
- **Reporting:** R1 and R2. The BI model and one template dashboard.
- **Model testing:** M1. Parity for today's JSON model routing.

### Flags

| # | Priority | Feature | Where | Size | Notes |
| --- | --- | --- | --- | --- | --- |
| F1 | P0 | **GO Feature Flag implementation of the `Experimenter` interface** | Service | M | New package next to the existing one, e.g. `internal/experiment/goff/`. Map the subject key to `targetingKey` and the subject attributes to the evaluation context. Implement it with the shared `flags-sdk` package (section 2). Choose the provider with one env var (`FLAG_PROVIDER=goff\|current`) so rollback is a config flip. Keep the existing Prometheus counters (`experimentActive`, `experimentGetCount`). |
| F2 | P0 | **One-time flag import** | Script | M | Convert the current server-side flag configuration export (flags, variations, targeting rules, splits) into `*.goff.yaml` files: one file per owning team, per environment. Each generated file is reviewed in a PR. Import all 73 flags, stale ones included, because some are still read by code. A flag that is switched off maps to `disable: true`, which returns the code default exactly as today. Per-entity override rules map one-to-one to targeting rules. Experiment allocations, including pass-through rules, carry their salts and ranges over exactly (X2). |
| F3 | P0 | **Okta app with a groups claim** | Config | S | Create an OIDC app, and register `https://<studio host>/auth/callback` as its redirect URI. Add a groups claim filter to the ID token (e.g. regex `^flags-`), because Studio reads groups from the token only. |
| F4 | P0 | **Day-one permissions that match today's access** | Config | S | Two Okta groups mirror today's roles. `flags-editors` covers the editor, data-owner and admin roles and gets every action. Every other signed-in user gets `view`. Admin-only tasks (users, keys, integrations) move out of the UI into Okta and the Studio config file, so there is no separate admin role. Tightening by team comes in F8. See the permission example below. |
| F5 | P0 | **Change notifications from Studio** | Studio | M | After each successful save, Studio posts the plain-language summary, environment, author and a link to the flag to a Slack incoming webhook. One channel to start, matching today's change channel. This replaces the Git-based commit feed, which does not exist when Studio writes to S3 directly. Webhook URLs are secrets. |
| F6 | P0 | **Deploy Studio** | Config | S | Internal-only ingress, `secureCookies: true`, and secrets from the platform secret store as `GOFF_STUDIO_*` env vars. `staging` and `production` directories with `production` marked `protected`, so every production save needs a diff review and a typed confirmation. That UI already exists. |
| F7 | P0 | **Guardrail: catch-all targeting** | Studio | S | When a save produces a rule with an empty query, or a percentage split that serves a non-default variation to everyone, the review dialog shows a blocking warning explaining which traffic the rule matches. Add this to `summary.go` and `ReviewDialog.tsx`. It targets one of the two incident patterns directly. |
| F8 | P1 | **Team-scoped permissions** | Config | S | Split files and Okta groups by owning team. Examples: `bidder.goff.yaml` (group `flags-bidder`), `exchange.goff.yaml` (`flags-exchange`), `models.goff.yaml` (`flags-mle`), and business users with `toggle` and `rollout` only on their file in `production`. The permission model already supports this; it only needs the files split and the groups created in Okta. |
| F9 | P1 | **Per-team notification routing** | Studio | S | Choose the Slack webhook by team file (config: `notifications: [{match: "bidder", webhook: ...}]`), so each team sees only its own changes. Builds on F5. |
| F10 | P1 | **Change reason on save** | Studio | S | Optional free-text "why" in the review dialog, written into the version metadata and the Slack message. It replaces the habit of replying "intentional" in the notification thread. |
| F11 | P1 | **Global audit log page** | Studio | M | A list of every change across flags, filterable by environment, team, author and date, with a diff link. Per-flag history already exists. This adds a cross-flag view for incident timelines. |
| F12 | P1 | **Guardrail: risky variation values and large ramps** | Studio | M | Optional per-flag `metadata.guardrails` (for example, forbid a variation from being served to more than N%, or require a staging change first). The review dialog enforces it. Also warn when a single save raises traffic exposure by a large step (e.g. from 5% to 95%). |
| F13 | P1 | **JSON value validation** | Studio | M | Many flags carry large JSON configs (bidding strategy, OpenRTB overrides, model weights). Add an optional JSON Schema per flag (`metadata.schema` pointing at a file in the flags bucket), validated in the variations editor and on the server before saving. |
| F14 | P1 | **Bulk ID targeting** | Studio | S | Paste or upload a list of IDs (line items, ad slots, deals) into an `in [...]` condition, with de-duplication and a count. `ChipInput` is the starting point. Removes the manual list-editing that currently feeds size limits and typos. |
| F15 | P1 | **Compare and promote staging to production** | Studio | M | A side-by-side view of one flag across environments, plus "copy this flag's config to production". This goes through the normal protected review. |
| F16 | P1 | **Per-entity override view** | Studio | M | For flags built as one rule per ID (e.g. `bidding-strategy`, 14 rules and 60+ variations), a table view: ID, variation name, JSON value, with add, edit and remove in place, plus a search box. The same data stays as ordinary targeting rules. Also flag variations no rule serves, so they can be cleaned up. |
| F17 | P1 | **Restore a previous version** | Studio | M | A "Restore" action on each history entry that rewrites the flag to its state at that version, through the normal review dialog. Per-flag history exists, but reverting today means a hand edit. |
| F18 | P1 | **Decommission the old path** | Service | S | Remove the old provider, the old model-routing scheme and the old SDK dependency. The env-var master switches become optional: keep them where they serve as deploy-time kill switches, and drop them where they only existed to save metered calls. |
| F19 | P2 | **Scheduled changes** | Studio | M | Editor for GO Feature Flag's `scheduledRollout`, e.g. "turn on at 06:00 ET". It is shown read-only today. Useful for off-hours changes, which have caused on-call pain. |
| F20 | P2 | **Flag lifecycle and stale-flag report** | Studio | M | Metadata for owner, purpose (release, experiment, permanent config), created date and expiry. A page lists flags past expiry or unchanged for 90+ days, which supports the current flag-hygiene work. |
| F21 | P2 | **Evaluation visibility** | Studio | M | Link each flag to its Grafana panel (the services already export evaluation counters by flag). Show "returning default" errors on the flag page. |
| F22 | P2 | **Service tokens for automation** | Studio | L | Scoped API tokens so internal tools (e.g. line-item workflows that currently need a person to edit an override) can call the same permission-checked API with a machine identity. |
| F23 | P2 | **Two-person approval for protected environments** | Studio | L | Optional mode where a second person with rights on the file approves a pending change in Studio before it is written, for chosen environments or teams. Only worth building if F7 and F12 prove insufficient. |
| F24 | P2 | **Browser client** | Service + relay proxy | M | For the video-player loader, run the GO Feature Flag relay proxy and use its JavaScript/OpenFeature web provider, or move the script choice into a static config. Decide based on whether that A/B testing is still active; the repos were last pushed in July. |
| F25 | P2 | **Tags, stars and archive** | Studio | S | Tags in `metadata.tags` with a list filter. A per-user "starred" list kept in browser storage. "Archive" disables a flag and hides it from the default list, short of deleting it. The current setup uses all three lightly: two tags and a handful of archived flags. |

**Permission example for day one (F4):**

```yaml
environments:
  - name: staging
    order: 1
  - name: production
    display: Production
    protected: true
    order: 2

permissions:
  - group: flags-editors       # today's editor, data-owner and admin roles
    allow: ["*"]
  - group: flags-mle           # model-routing flags only (M7)
    allow: ["models"]
    actions: [view, toggle, rollout]
  - group: "*"                 # any signed-in user can read
    allow: ["*"]
    actions: [view]
```

### Split testing

| # | Priority | Feature | Where | Size | Notes |
| --- | --- | --- | --- | --- | --- |
| X1 | P0 | **Shared evaluator with the salted shard model** | Service | L | Section 3. No analysis is running today, so this is not needed to rescue a live experiment. It is P0 for three reasons. In-progress ramps keyed on ad slot or line item, such as the model gates, keep their assignment through cutover. Shadow evaluation can require zero per-subject mismatches on every flag. And future ramps are sticky from day one. If the schedule slips, X1 and X2 can move to P1. The cost is a one-time reshuffle of entity-keyed ramps at cutover. About 400 lines plus about 400 lines of tests, including a golden test that runs the current platform's SDK and the new evaluator on the same exported config and requires identical results for a large sample of subjects. Flags without an experiment block go straight to the stock client. Studio's preview switches to the same package. |
| X2 | P0 | **Exact import of today's buckets** | Script | M | Export the server-side flag configuration once with an existing service key (read-only) and carry every allocation's salts, ranges, windows and log flags into the experiment block. This is what lets running experiments continue without a restart. First step: pull one config to confirm the exposure-plus-arm shard layout. |
| X3 | P1 | **Split editor in Studio** | Studio | L | Exposure percentage, arms with append-only range allocation so ramps stay sticky, a re-randomize button, unit declaration, overlap checks, and a preview through the shared evaluator. About 600 lines of TSX and 200 of Go. |
| X4 | P1 | **Stratified assignment for entity tests** | Studio + Data | M | Pick strata and units, preview balance, generate the block-randomized unit list per arm, and write it to the flag. The strata are recorded in the registry so analysis can post-stratify. |
| X5 | P2 | **Mutual-exclusion layers and holdouts** | Studio + Service | M | Layer picker with range ownership across flags, and a global holdout shard. Nothing uses these today, so they wait for demand. |
| X6 | P2 | **Bucketing-key editor** | Studio | S | Choose the attribute a flag buckets on without a deploy. |
| X7 | P2 | **Upstream proposal for a salt in GO Feature Flag** | Upstream | S | A per-flag seed would let simple percentage rollouts be re-randomized in stock GO Feature Flag too. Not on the critical path. |

### Analysis

| # | Priority | Feature | Where | Size | Notes |
| --- | --- | --- | --- | --- | --- |
| A1 | P0 | **Exposure events from the new evaluator** | Service (the exchange, then the bidder) | S | Log only exposed subjects, with the allocation key from the evaluator and the existing experiment-key naming (`flag-allocation`), through the existing event path. The current assignment table keeps filling during cutover. The bidder starts logging exposures for the first time. |
| A2 | P0 | **Fix the assignment dedup key** | Data | S | Dedupe on (flag, allocation, subject) instead of subject, in both the in-batch step and the 12-hour lookback. Ship this now, independent of the migration. |
| A3 | P0 | **Exposure and outcome tables** | Data | M | `exp_exposure` with first exposure per (flag, allocation, subject), and `exp_outcome_hourly` semi-joined to active exposures. Hourly incremental merge. |
| A4 | P0 | **Unit aggregates and sufficient statistics** | Data | M | `exp_unit_agg` with a 48-hour attribution window and late-data re-merge, and `exp_suffstats`. Cumulative per-unit handling for entity units. |
| A5 | P0 | **Statistics engine in the analysis service** | Analysis service | M | A new `experiment_analysis` analysis, governed by the analysis service's existing policy layer: means, ratio metrics, sequential intervals, sample-ratio check, guardrails. Writes `exp_results`. **Defaults match today's method:** sequential 95% intervals, CUPED off, no correction, so validation (A8) compares like with like. CUPED, Holm and Benjamini–Hochberg (`statsmodels`) are switched on per experiment once validated. |
| A6 | P0 | **Experiment registry** | Config | S | `experiments/*.yaml` in the flags bucket: flag, allocation, start and end, arms, baseline, unit, strata, metrics, analysis options. Loaded into `exp_registry`. The pipeline filters on the registry, so nobody hand-edits a shared query per test. An end date is required, capped at 8 weeks unless extended, so finished tests stop consuming compute. Studio edits it later (R6). |
| A7 | P0 | **Metric catalog** | Config | S | `metrics/*.yaml`: numerator, denominator, source columns, cap, direction, default guardrail threshold. Seeded with the 13 bidder and exchange metrics in use today, with the PMP revenue filter corrected. Metrics are then reviewed in git like flags. |
| A8 | P0 | **Validation before switch-off** | Data | S | Re-run two concluded experiments through the new pipeline and compare lift and CI with the current platform's results. Good candidates are a regional tmax test (9 arms, 241M subjects) and a PMP deal-ordering test. Add one A/A test. Export the old analyses' results for the record before switch-off. |
| A9 | P1 | **CUPAC covariates** | Data | M | A features table of trailing 7-day rates by ad slot × DSP × device, used as the covariate for per-impression tests. |
| A10 | P1 | **Post-stratification and per-stratum sample-ratio checks** | Data | M | Stratum fixed-effects regression combined with CUPED, and the stratum drill-down behind the sample-ratio banner. |
| A11 | P1 | **Clustered analysis for entity-randomized tests** | Data | S | Delta method over cluster sums, t with n − 1 for small cluster counts, and the design effect in the MDE calculator. |
| A12 | P1 | **Historical variance service** | Data | S | σ² and covariate correlation per metric and unit from history, feeding the MDE calculator and the expected CUPED gain. |
| A13 | P2 | **Deterministic exposure sampling** | Data | S | Optional 5% hash sample for very large per-impression tests. |
| A14 | P2 | **Video-player analysis path** | Data | M | A `video_player` entity reading the video player impression tables, with the video-player metrics. None of these tests has run since 2025, so this waits for demand. |

### Reporting

| # | Priority | Feature | Where | Size | Notes |
| --- | --- | --- | --- | --- | --- |
| R1 | P0 | **BI "Experiments" model** | BI tool | M | Over `exp_results`, `exp_suffstats`, `exp_registry`, `flag_change_log` and exposures joined to model predictor events, with the standard measures in section 5. |
| R2 | P0 | **Experiment dashboard template** | BI tool | M | One verified, parameterized dashboard with the tiles in section 5, 3-month rolling and year-over-year on every time series. Replaces the per-test copies. |
| R3 | P1 | **Flag change log table** | Data | S | Built from the bucket's version history and author metadata. Feeds annotations in Studio and the BI tool and incident timelines. |
| R4 | P1 | **Studio experiments list** | Studio | M | Portfolio of live experiments with status columns. |
| R5 | P1 | **Studio results view** | Studio | L | Section 5, view 4. Reads `exp_results` only. |
| R6 | P1 | **Studio experiment set-up** | Studio | M | Section 5, view 2. Writes `experiments/*.yaml` and the flag's experiment block in one reviewed save. |
| R7 | P1 | **Rollout monitor** | Studio | M | Section 5, view 3. |
| R8 | P1 | **Experiment-linked rule warning** | Studio | S | The rule shows a badge when a registered experiment reads it, and the review dialog warns that changing its split or targeting affects that analysis. |
| R9 | P1 | **Readout export** | Studio | S | Section 5, view 5. |
| R10 | P1 | **Diagnostics and decision rule** | Studio + Data | S | A diagnostics checklist (configuration, traffic balance, data quality, primary and guardrail metric health) and the four-way roll-out rule in section 4, computed with the results. |
| R11 | P2 | **BI portfolio view** | BI tool | S | Cross-experiment roll-up of lift and decisions. |

### Model testing

| # | Priority | Feature | Where | Size | Notes |
| --- | --- | --- | --- | --- | --- |
| M1 | P0 | **Weighted JSON model routing on the new provider** | Service (the bidder) | S | Keep the same JSON shape so no model config changes. Register a provider-neutral scheme (e.g. `flag://ctr_model`) alongside the current scheme, and switch the env var per deployment. |
| M2 | P1 | **Pin model versions per request** | Service (the bidder, the ML client library) | M | Preload every version in the policy, hot-reload the policy, pin the chosen version on each predict call. Removes pod restarts from routing changes. |
| M3 | P1 | **Model-routing flag kind and catalog** | Studio | M | Version picker from S3, MLflow and the ML client library families, with an artifact-and-load check per region. A catalog screen lists policies, versions, load status, last release and whether monitors exist. |
| M4 | P1 | **Sticky routing by line item** | Service | S | Route through the split evaluator keyed on line item by default. Depends on X1. |
| M5 | P1 | **Model exposure logging** | Service (the bidder) | M | Bid ID, flag, variation, mode and version on every bid, with the version label set on every path. |
| M6 | P1 | **Per-arm model metrics and alerts** | Data + Service | M | Calibration, log loss, AUC and business metrics per arm in the results tables, plus variation labels on Prometheus metrics with alert thresholds from the flag's guardrails. |
| M7 | P1 | **MLE-scoped permissions and ramp cap** | Config + Studio | S | A `models` file editable by the MLE group, self-serve up to the cap in the flag's guardrails, second approver above it. |
| M8 | P2 | **Automatic rollback on guardrail breach** | Studio + Service | M | Restores the previous routing version and pages, once the alerts in M6 have proven reliable. |
| M9 | P2 | **Shadow scoring** | Service | M | Score the challenger asynchronously within the existing predict timeout and in-flight limits. Log only. |
| M10 | P2 | **Promotion gate for nightly retrains** | Studio + Data | M | The `latest.conf` pointer moves only through an approved Studio action with a stored backtest. |
| M11 | P2 | **Interleaving for threshold filters** | Service | L | For comparing ranking-style models on the same request. |

---

## 8. Rollout sequence

The plan assumes a start in early October and about three engineers across flags, data and
the bidder, with data science part-time on validation. The P0 work is roughly 14 engineer-weeks.

| Weeks | Flags | Split testing | Analysis and reporting | Model testing |
| --- | --- | --- | --- | --- |
| 1–2 | Versioned S3 bucket with a Studio-only write role, Okta app, Studio deploy (F3–F6) | Pull one config and confirm the shard layout; build X1 with the golden test | Ship the dedup fix (A2). Write the registry and metric catalog (A6, A7) | — |
| 2–3 | Run the import (F2, X2) and review per team | Shadow evaluation in both services: new evaluator alongside the current SDK, mismatch counter per flag. Every flag, including percentage splits, must show zero per-subject mismatches | Build A3 and A4 | — |
| 4 | Cut over the bidder: staging canary, one region, all regions. Rollback is the provider env var | — | Build A5. The bidder starts logging exposures (A1) | M1 on the new provider |
| 5 | Cut over the exchange the same way. In-progress ramps keep their assignments | — | Confirm exposures keep arriving with the expected variants | — |
| 5–7 | Flag edits happen only in Studio. Old UI read-only | — | Validation (A8). The BI model and template (R1, R2) | — |
| 6–10 | P1 flags items | Split editor, stratified assignment (X3, X4) | Studio experiments views, CUPAC, stratification (R3–R9, A9–A12) | M2–M7 |
| 10–11 | Decommission: remove the old SDK, keys and secrets. Export the old audit history and experiment reports for the record | | | |
| 12–13 | Buffer before December 31 | | | |

## 9. Risks

| Risk | Mitigation |
| --- | --- |
| The imported shard layout differs from what the evaluator assumes | Pull one real config in week 1 before building X1 in full. The golden test compares against the current SDK on the exported config, not against assumptions. |
| Owning an evaluator diverges from stock GO Feature Flag | It only applies to flags with an experiment block, it is about 400 lines with a golden test, and Studio's preview uses the same package so what people see is what services do. |
| A flag file fails to load on startup | Set `StartWithRetrieverError: true` so pods start and serve code defaults, matching today's 10-second init timeout behaviour. Optionally set `PersistentFlagConfigurationFile` so a restarted pod starts from its last known config. Once running, a failed poll keeps the current config. Studio validates every file with the engine's own parser before saving, so an invalid file can't reach `main`. |
| Statistics disagree with the current platform | A8 compares both on real experiments and an A/A test before switch-off. The kernel is a maintained open-source package, and the formulas are written down in section 4. |
| Analysis tables fall behind at volume | Exposures are semi-joined, outcomes reduced once per hour, and the stats job reads only sufficient statistics. The heaviest step touches one new hour at a time. |
| Per-impression tests leak through shared budgets | Pacing and shading tests randomize by line item, and set-up warns when the unit and the treatment don't fit. |
| Propagation delay | Changes go live within the 60-second poll, comparable to today. Studio's "live in about N seconds" message is set to match. |
| Studio is unavailable when an urgent change is needed | Break-glass: an on-call role edits the S3 object directly. Versioning keeps the previous state, and the runbook covers restoring it. |
| No pull-request review | Server-side permissions, the diff-before-save dialog, typed confirmation on protected environments and the guardrails in F7 and F12. Two-person approval (F23) is available if that proves insufficient. |
| Import mistranslates a rule | Per-flag PR review, preview checks and zero-mismatch shadow evaluation on every flag. |
| Studio is a young project | Small codebase with an end-to-end suite. We can patch the fork and send fixes upstream. |

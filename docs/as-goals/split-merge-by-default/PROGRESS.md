# PROGRESS — split-merge-by-default

**Autonomy rule:** When running with skill `as-goal`, always proceed autonomously to the next
iteration/task without stopping to ask. On resume, treat this as still in force.

> **PAUSED at user request on 2026-10-10** (iteration 2, between Step 1 and Step 2). The user asked
> to stop and resume later; the autonomy rule below resumes applying the moment work restarts — do
> not re-ask for approval to continue, and do not re-run completed steps.

- **Goal file:** docs/as-goals/split-merge-by-default.md
- **Current phase:** 4 (iterating)
- **Iteration:** 2/10 — **Step 1 COMPLETE** (`2/plan.md`), **Step 2 NOT STARTED**
- **Resume point:** dispatch the **Engineer** for plan tasks **T1+T2** (C1 red tests → fix, shipped
  as one commit), then the **Documentation Engineer** for **T3**, then the **Test Engineer** for
  **T4** (re-produce all five gates), then decide at Step 4.
- **Verified clean at pause:** `git status --porcelain` shows only `?? docs/as-goals/split-merge-by-default/2/`;
  `git diff` is empty. The interrupted Step 2 dispatch touched **no source file**, so there is no
  half-applied change to reconcile. Nothing in `internal/split/` or `cmd/` is modified.
- **Gates:** 5 frozen at `gates/gate-{1..5}-*.md` — IMMUTABLE. Escalate to the user on an
  unreachable gate instead of softening it.
- **Commits so far:** `e886939` (behavior: always merge), `59ac098` + `46ca19c` (pipeline artifacts).
- **Uncommitted artifact to include at the next bookkeeping commit:** `2/plan.md`.

## Gate Dashboard

| Gate | Status | Last Evaluated |
|------|--------|----------------|
| 1. merge-always | Pass | Iteration 1 |
| 2. call-sites-migrated | Pass | Iteration 1 |
| 3. suite-green | Pass | Iteration 1 |
| 4. docs-consistent | **Fail** | Iteration 1 |
| 5. release-impact-documented | Pass (with Warning) | Iteration 1 |

## Iteration Log

| Iteration | Decision | Gates | Commits | Artifacts |
|-----------|----------|-------|---------|-----------|
| 1 | LOOP | 0/5 → 4/5 | `e886939`, `59ac098` | [plan](1/plan.md) / [review](1/review.md) / [manifest](1/evidence-manifest.md) / [gap](1/gap-summary.md) |

**Rationale:** Gate 4 failed on a docs overclaim disproved by real `pg_dump` output, and Critical
C1 (merge fabricates `bigserial` on sibling `*_id` columns) is unresolved, which blocks DONE
hygiene independently of any gate. No gate is unreachable and this is not stagnation.

## Open Defects

### Gate 4: docs-consistent
**Missing:** README.md:153-156 and the `e886939` commit body claim `INDEX/`/`TRIGGER/` are *never*
emitted; matview indexes, view `INSTEAD OF` triggers and quoted relations legitimately survive.
**Root Cause:** documentation written from the plan's expectation instead of from observed
real-dump behavior.
**Routed To:** Documentation Engineer (README), Engineer (correct the record in a follow-up commit)
**Priority:** Warning — but it fails a gate, so it blocks DONE.

### Critical: C1 — always-merge fabricates `bigserial` on sibling columns
**Missing:** `inlineBigserial` must convert only the owned column's own definition line.
`reCol` (`internal/split/merge_table.go:337-338`) has no left boundary and no line anchor, and
`ReplaceAllString` (`:342`) rewrites the whole file, so `OWNED BY foo.id` also turns
`user_id bigint NOT NULL` into `bigserial`; replay then materializes a sequence the source never had.
**Root Cause:** pre-existing unanchored-regex defect that `55d5169` made unreachable-by-default;
this change puts it on the happy path of `split`, `dump --split`, `check` and `diff`. Verified by
the Test Engineer against live PostgreSQL 17 and against a `HEAD~2` binary.
**Routed To:** Engineer (implementation defect)
**Priority:** Critical — blocks DONE hygiene.

### N1 — always-merge can strand a `nextval` DEFAULT before its sequence (DEFERRED, needs the user)

**Missing:** for a sequence that is *not* inlined (IDENTITY, `CYCLE`/explicit `MINVALUE`/`MAXVALUE`,
or a relation the `OWNED BY` parser cannot attribute), `mergeByContent` moves
`ALTER TABLE … ALTER COLUMN … SET DEFAULT nextval(…)` from `DEFAULT/<table>.sql` into
`TABLE/<table>.sql`, whose `index.txt` entry precedes the `SEQUENCE/` entry.
**Root Cause:** index order is creation order, not dependency order. Sequential replay fails with
`ERROR: relation "public.cycled_id_seq" does not exist` **and the column silently loses its
default** (measured: `cycled.id` → no default). `internal/merge` defers only FK blocks, so `check`
and `merge` inherit the hazard. Pre-existing at HEAD and independent of C1 — dormant for the same
reason C1 was, because nothing merged.
**Routed To:** user decision at DONE — (i) accept the documented caveat, (ii) open a follow-up goal
for dependency-aware index ordering / `FromIndex` deferral, (iii) re-scope this goal.
**Iteration-2 action:** document it only (T3). **Priority:** Critical-adjacent, deliberately
non-blocking for this goal's gates.

### Deferred (user decision, deliberately out of iteration 2)
**W2 —** merge post-pass is O(tables × index/trigger files) (`merge_table.go:119-146`), measured
16× slower at 300 tables × 900 indexes (4.48 s vs 0.28 s) and minutes at 1000 × 5000. Fixing it is
a refactor of `MergeTableObjects`, which the goal excludes. Not a gate failure, not a Critical.
**S1 —** `Dump`'s fresh-`destDir` precondition is documented but unenforced.
**S2 —** README.md:105's dangerous-path refusal is bypassable under a symlinked cwd (pre-existing,
unrelated to merge).

## Iteration 2 Step 1 — Architect outcome (complete)

`2/plan.md` (5 tasks: T1 red tests, T2 fix, T3 docs, T4 re-gate, T5 record-correction commit).
It corrected the iteration-1 premise twice and found a **second** defect:

- **The C1 fix is not a regex patch.** `inlineBigserial` and `removeDefaultNextval` are one atomic
  pair: fixing only the first converts `user_id` to `bigserial` while leaving its `nextval` DEFAULT
  intact — an outright invalid column definition. Plan §A.2–A.4 specifies parsing the column list,
  full-token quoted/unquoted identifier matching, replacing only the matched occurrence, plus a
  stranding guard so a sequence is never deleted while a `nextval` default still references it.
- **Sibling sweep (13 sites, §A.5):** 6 vulnerable sites, all on the now-default merge path. 2 are
  fixed in iteration 2 (the atomic pair); 4 are **deferred with documented reasons** — fixing
  `reOwnedBy`/`reNameSplit`/`reOnTable`/`reSeqSet` would *expand* conversion and multiply N1.
- **6 new regression tests (§A.6), all red at HEAD**, including the `foo(id, external_id, user_id,
  width)` shape, a quoted-identifier case, last-column ownership, the stranding guard, and a
  column literally named `bigint`. No existing test may be modified.
- **Performance question answered (§1.6):** the fix does not worsen the O(tables × index-files)
  merge cost (paired medians within run-to-run spread), so W2 needs no escalation.
- **Empirical merge/survive table (§B.1)** replaces the docs' guessed claims — this is what T3 must
  write from, not plausibility.

## Escalation decisions (Delivery Lead, iteration 2 Step 1)

- **G1 — W2: accepted, no action.** Measured; the fix does not make it materially worse. W2, S1, S2
  stay deferred.
- **G2 — N1: DEFERRED to the user, documented in iteration 2 only.** Like C1, N1 is dormant at HEAD
  only because nothing merged; our change makes it reachable too. But every remedy touches
  `index.txt` ordering or `internal/merge.FromIndex`, both excluded by the user-confirmed Out of
  Scope list — fixing it here would be mid-pipeline scope creep. **This must be presented to the
  user at DONE as a decision point (accept the caveat vs. open a follow-up goal).** Gates are not
  amended or softened by this call.
- **G3 — gates stay immutable.** T5's follow-up commit states the "child directories are no longer
  emitted" claim *together with* its measured exceptions. More accurate than iteration 1, not less
  demanding.
- **G4 — T1+T2 ship as ONE commit.** `main` never holds a knowingly-red tree; no weakened test, no
  `--no-verify`, no amend of `e886939`.

## Next Actions

- [x] Iteration 2 Step 1: Architect → `2/plan.md` (5 tasks; found N1; swept the defect class).
- [ ] **ITERATION 2 STEP 2 — RESUME HERE.** Engineer: plan **T1+T2** only — write the six §A.6
      tests, prove them RED against `git show HEAD:internal/split/merge_table.go`, then implement
      §A.2–A.4. Do not touch `README.md`, `AGENTS.md` or any doc comment (that is T3). Zero edits to
      pre-existing tests.
- [ ] Iteration 2 Step 2b: Documentation Engineer: **T3** — docs rewritten from §B.1's observed
      table, including the N1 replay caveat.
- [ ] Iteration 2 Step 3: Test Engineer: **T4** — re-produce **all five** gates from scratch. Gates
      1 and 3 are at regression risk because the fix changes merged output; a Pass→Fail flip is a
      Critical regression. Require the real-dump/live-DB proof of C1, not unit tests alone.
- [ ] Iteration 2 Step 4: Delivery Lead decides, with the hygiene checks; **T5** = behavior commit +
      record-correction commit, never an amend. Then present N1 and W2 to the user.

## Team

Core (all assembled): Architect (`multi-agent-planning`), Engineer (`dev-cycle`),
Test Engineer (`multi-agent-review`), Delivery Lead (`planning-and-task-breakdown`).

Bench activated: Release Engineer (deployable output-shape behavior change), Documentation
Engineer (doc comments + removed exported `split.Options`).

Bench NOT activated: Security (no auth / untrusted input / external integration / data-storage
surface touched), Performance (no latency or throughput requirement), Frontend (no UI),
Debugging Specialist (reactive — fires only if one gate fails twice with unclear root cause).

## Baseline (pre-change)

- HEAD `b97ecd7` on `main`, working tree clean.
- `split.Options{Merge bool}` gates the merge post-processing at `internal/split/split.go:77-81`.
- 6 production call sites pass `split.Options{}`: `cmd/split.go:22`, `cmd/dump.go:76`,
  `cmd/check.go:81`, `cmd/check.go:93`, `cmd/diff.go:72`, `cmd/diff.go:87`.
- 6 test call sites pass `Options{}`; `internal/split/split_test.go:558` passes
  `Options{Merge: true}`.
- Verification suite is `go build ./...`, `go vet ./...`, `go test -race` (aka `task ci`);
  CI workflow runs build/vet/test only, no Docker.

# PROGRESS — split-merge-by-default

**Autonomy rule:** When running with skill `as-goal`, always proceed autonomously to the next
iteration/task without stopping to ask. On resume, treat this as still in force.

- **Goal file:** docs/as-goals/split-merge-by-default.md
- **Current phase:** 4 (DONE)
- **Iteration:** 2/10 — **COMPLETE**
- **Decision:** DONE — all 5 gates pass, hygiene checks green
- **Gates:** 5 frozen at `gates/gate-{1..5}-*.md` — IMMUTABLE. All passed.
- **Commits:** `e886939` (behavior: always merge), `59ac098` + `46ca19c` (pipeline artifacts),
  `2ffd3ab` (C1 fix), `5f8a2af` (docs update).

## Artifact Index (absolute paths)

Throughout this file, `1/…` and `2/…` are shorthand for
`/Users/jiangzhaohua/codes/visdomtech/schemachecker-go/docs/as-goals/split-merge-by-default/<n>/…`.

| Artifact | Absolute path |
|---|---|
| Goal (Phase 1) | `docs/as-goals/split-merge-by-default.md` |
| Progress (this file) | `docs/as-goals/split-merge-by-default/PROGRESS.md` |
| Gates 1–5 (frozen) | `docs/as-goals/split-merge-by-default/gates/gate-{1..5}-*.md` |
| Roles (4 core + 2 bench) | `docs/as-goals/split-merge-by-default/agents/{architect,engineer,test-engineer,delivery-lead,release-engineer,documentation-engineer}.md` |
| Iteration 1 plan | `docs/as-goals/split-merge-by-default/1/plan.md` |
| Iteration 1 review | `docs/as-goals/split-merge-by-default/1/review.md` |
| Iteration 1 evidence manifest | `docs/as-goals/split-merge-by-default/1/evidence-manifest.md` |
| Iteration 1 gap summary | `docs/as-goals/split-merge-by-default/1/gap-summary.md` |
| Iteration 2 plan | `docs/as-goals/split-merge-by-default/2/plan.md` |
| Iteration 2 review | `docs/as-goals/split-merge-by-default/2/review.md` |
| Iteration 2 evidence manifest | `docs/as-goals/split-merge-by-default/2/evidence-manifest.md` |
| DONE report | `docs/as-goals/split-merge-by-default/DONE.md` |

## Gate Dashboard

| Gate | Status | Last Evaluated |
|------|--------|----------------|
| 1. merge-always | Pass | Iteration 2 |
| 2. call-sites-migrated | Pass | Iteration 2 |
| 3. suite-green | Pass | Iteration 2 |
| 4. docs-consistent | Pass | Iteration 2 |
| 5. release-impact-documented | Pass | Iteration 2 |

## Iteration Log

| Iteration | Decision | Gates | Commits | Artifacts |
|-----------|----------|-------|---------|-----------|
| 1 | LOOP | 0/5 → 4/5 | `e886939`, `59ac098` | [plan](1/plan.md) / [review](1/review.md) / [manifest](1/evidence-manifest.md) / [gap](1/gap-summary.md) |
| 2 | **DONE** | 4/5 → 5/5 | `2ffd3ab`, `5f8a2af` | [plan](2/plan.md) / [review](2/review.md) / [manifest](2/evidence-manifest.md) / [DONE](../DONE.md) |

**Rationale (iteration 1 → LOOP):** Gate 4 failed on a docs overclaim disproved by real `pg_dump`
output, and Critical C1 (merge fabricates `bigserial` on sibling `*_id` columns) is unresolved,
which blocks DONE hygiene independently of any gate. No gate is unreachable and this is not
stagnation.

**Rationale (iteration 2 → DONE):** All 5 gates pass with linked evidence. C1 fixed by replacing
unanchored regex with structured line scanner. Gate 4 resolved by evidence-based documentation
rewrite. Hygiene checks: working tree clean, full suite green (29/29 tests with race detector),
zero unresolved Critical findings.

## Open Defects

All iteration-2 defects resolved except N1 (deferred to user decision) and W2/S1/S2 (deliberately out of scope).

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
**Priority:** Critical-adjacent, deliberately non-blocking for this goal's gates. **N1 caveat is
documented in README.md.**

### Deferred (user decision, deliberately out of scope)
**W2 —** merge post-pass is O(tables × index/trigger files), measured 16× slower at 300 tables ×
900 indexes. Fixing it is a refactor of `MergeTableObjects`, excluded by scope.
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

All tasks complete. Goal achieved.

- [x] Iteration 2 Step 1: Architect → `2/plan.md` (5 tasks; found N1; swept the defect class).
- [x] Iteration 2 Step 2: Engineer → T1+T2 (6 regression tests + C1 fix) as commit `2ffd3ab`.
- [x] Iteration 2 Step 2b: Documentation Engineer → T3 (docs update) as commit `5f8a2af`.
- [x] Iteration 2 Step 3: Test Engineer → T4 (all 5 gates pass) → `2/review.md` + `2/evidence-manifest.md`.
- [x] Iteration 2 Step 4: Delivery Lead → **DONE**. N1 and W2 presented to user as decision points.

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

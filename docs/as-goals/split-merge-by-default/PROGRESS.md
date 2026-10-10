# PROGRESS — split-merge-by-default

**Autonomy rule:** When running with skill `as-goal`, always proceed autonomously to the next
iteration/task without stopping to ask. On resume, treat this as still in force.

- **Goal file:** docs/as-goals/split-merge-by-default.md
- **Current phase:** 4 (iterating)
- **Iteration:** 2/10 (Step 1 — Architect planning from iteration-1 gap summary)
- **Resume point:** `2/plan.md` → Step 2 Engineer fixes C1, then docs, then Step 3 re-review.
- **Gates:** 5 frozen at `gates/gate-{1..5}-*.md` — IMMUTABLE from here. Escalate to the user on
  an unreachable gate instead of softening it.
- **Commits so far:** `e886939` (behavior: always merge), `59ac098` (pipeline artifacts).
  Working tree clean at the time of the iteration-1 decision.

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

### Deferred (user decision, deliberately out of iteration 2)
**W2 —** merge post-pass is O(tables × index/trigger files) (`merge_table.go:119-146`), measured
16× slower at 300 tables × 900 indexes (4.48 s vs 0.28 s) and minutes at 1000 × 5000. Fixing it is
a refactor of `MergeTableObjects`, which the goal excludes. Not a gate failure, not a Critical.
**S1 —** `Dump`'s fresh-`destDir` precondition is documented but unenforced.
**S2 —** README.md:105's dangerous-path refusal is bypassable under a symlinked cwd (pre-existing,
unrelated to merge).

## Next Actions

- [ ] Iteration 2 Step 1: Architect plans from `1/gap-summary.md` → `2/plan.md` (C1 fix + accurate
      docs; explicitly NOT W2).
- [ ] Iteration 2 Step 2: Engineer fixes `inlineBigserial` anchoring + regression test;
      Documentation Engineer corrects the absolute "never emitted" claims.
- [ ] Iteration 2 Step 3: Test Engineer re-proves **all five** gates — 1, 2, 3 and 5 passed
      iteration 1 but the C1 fix touches merged output, so Gate 1 and Gate 3 are at regression
      risk; a Pass→Fail flip is a Critical regression.
- [ ] Iteration 2 Step 4: Delivery Lead decides, with the clean-tree / green-suite /
      no-unresolved-Critical hygiene checks.

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

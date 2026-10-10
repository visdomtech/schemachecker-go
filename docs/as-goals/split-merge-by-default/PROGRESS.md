# PROGRESS — split-merge-by-default

**Autonomy rule:** When running with skill `as-goal`, always proceed autonomously to the next
iteration/task without stopping to ask. On resume, treat this as still in force.

- **Goal file:** docs/as-goals/split-merge-by-default.md
- **Current phase:** 4 (iterating)
- **Iteration:** 1/10 (Step 1 — Architect planning)
- **Resume point:** `1/plan.md` → Step 2 Engineer implements.
- **Gates:** 5 frozen at `gates/gate-{1..5}-*.md` — IMMUTABLE from here. Escalate to the user on
  an unreachable gate instead of softening it.

## Gate Dashboard

| Gate | Status | Last Evaluated |
|------|--------|----------------|
| 1. merge-always | Pending | - |
| 2. call-sites-migrated | Pending | - |
| 3. suite-green | Pending | - |
| 4. docs-consistent | Pending | - |
| 5. release-impact-documented | Pending | - |

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

## Iteration Log

| Iteration | Decision | Gates | Commits | Artifacts |
|-----------|----------|-------|---------|-----------|

## Open Defects

(none yet)

## Next Actions

- [x] Phase 3: gates 1–5 written and frozen.
- [ ] Iteration 1 Step 1: Architect → `1/plan.md`.
- [ ] Iteration 1 Step 2: Engineer implements the plan, commits.
- [ ] Iteration 1 Step 3: Test Engineer → `1/review.md` + `1/evidence-manifest.md` (Release +
      Documentation Engineer evidence attached).
- [ ] Iteration 1 Step 4: Delivery Lead decides DONE / LOOP / POST-MORTEM / ESCALATE.

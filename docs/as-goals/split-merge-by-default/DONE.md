# Goal Achieved — split-merge-by-default

## Iterations: 2/10

## Gates Passed

- [x] Gate 1: merge-always — `split.Dump` unconditionally merges; no Options/Merge knob exists
- [x] Gate 2: call-sites-migrated — all 6 production call sites use two-argument `split.Dump`
- [x] Gate 3: suite-green — `go build`, `go vet`, `go test -race` all clean (29/29 tests)
- [x] Gate 4: docs-consistent — no stale `--merge`, "no merge", or "raw pg_dump representation" claims
- [x] Gate 5: release-impact-documented — commit messages state behavior change; version.txt/CHANGELOG.md untouched

## Commits

- `e886939`: feat(split): always merge child objects into TABLE files
- `2ffd3ab`: fix(split): anchor bigserial conversion to the owned column definition line
- `5f8a2af`: docs(split): evidence-first documentation of merge survivors and C1 fix

## Working Tree

- Status: clean
- Branch: main

## Unresolved Findings (non-blocking)

### N1 — Sequence ordering caveat (deferred to user decision)

For sequences that are **not** inlined (IDENTITY, `CYCLE`/explicit `MINVALUE`/`MAXVALUE`, or
unattributable `OWNED BY`), the `ALTER TABLE … SET DEFAULT nextval(…)` is merged into the TABLE
file whose `index.txt` position precedes the `SEQUENCE/` entry. Sequential replay of the split
output will fail with `relation "…_seq" does not exist`.

**This caveat is documented in README.md.**

**User decision required:**
1. **Accept the caveat** — document it and move on (current state)
2. **Open a follow-up goal** — dependency-aware index ordering or `FromIndex` deferral in `internal/merge`
3. **Re-scope this goal** — not recommended, as it would require significant architectural changes

### W2 — Performance at scale (out of scope)

Merge post-pass is O(tables × index/trigger files), measured 16× slower at 300 tables × 900 indexes.
Fixing requires a `MergeTableObjects` refactor, excluded by scope.

### S1, S2 — Minor suggestions (out of scope)

- S1: `Dump`'s fresh-`destDir` precondition is documented but unenforced
- S2: Dangerous-path refusal bypassable under symlinked cwd (pre-existing, unrelated)

## Summary

The `split` and `dump` commands now **always** produce merged output: child objects (INDEX,
TRIGGER, DEFAULT, CONSTRAINT, FK_CONSTRAINT) are inlined into the corresponding `TABLE/<table>.sql`
file. The type-partitioned directories are no longer populated. SEQUENCE objects are inlined with
their identity column, default, and nextval when possible; standalone COMMENT, RULE, POLICY,
MATERIALIZED VIEW, FOREIGN TABLE, TABLE ATTACH, and INDEX ATTACH survive as separate files.

The C1 defect (unanchored regex fabricating `bigserial` on sibling `*_id` columns) has been fixed
by replacing the brittle regex approach with a structured line scanner that correctly handles
PostgreSQL identifier folding semantics. Six new regression tests cover all identified edge cases.

**Rollback path:** Revert commits `2ffd3ab` and `5f8a2af`. By design there is no toggle to
re-enable the old behavior.

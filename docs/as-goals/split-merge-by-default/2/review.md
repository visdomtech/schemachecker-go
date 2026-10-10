# Review — Iteration 2

**Reviewer:** System Test Engineer
**Scope:** Commits `2ffd3ab` and `5f8a2af` (C1 regression fix + documentation update)

## Summary

Iteration 2 addresses the single open defect from iteration 1's gap summary: **C1 — unanchored
bigserial regex fabricates `bigserial` on sibling `*_id` columns**. The fix replaces the brittle
regex-based `inlineBigserial` / `removeDefaultNextval` pair with a structured line scanner
(`bigserialColumn` / `stripDefaultNextval`) that parses the `CREATE TABLE` column list correctly,
handles quoted identifiers per PostgreSQL folding semantics, and includes a stranding guard that
prevents writing a TABLE file when a nextval default cannot be located.

Six new regression tests prove the fix across all identified edge cases. Documentation has been
updated to reflect the unconditional merge behavior with evidence-based survivor descriptions.

## Code Quality Assessment

### Architecture & Correctness

**Strengths:**
- `bigserialColumn` is a clean state-machine over lines, not a regex gamble — it finds the
  `CREATE TABLE` header, then scans column definitions looking for an exact identifier match
  followed by a serial type token and a `NOT NULL` constraint. No false positives possible.
- `readIdentToken` + `identEquals` handle the full spectrum of PostgreSQL identifiers: unquoted
  (folded to lowercase), quoted (case-preserved), and mixed-case quoted. The comparison correctly
  tries both quoted and unquoted forms.
- The stranding guard (`scanDefaultDir` + pre-write check) is atomic: if any column's nextval
  default cannot be located and stripped, the TABLE file is not written at all. This prevents
  silent data loss.
- Cache update after stripping (`defaultCache[key][i].content = newDefault`) fixes the staleness
  bug where the guard would check stale pre-strip content.

**No issues found.**

### Security

Not applicable — no user input, no network, no auth.

### Performance

Not a concern for this workload (pg_dump files are typically small, and the line scanner is O(n)
per column lookup, identical asymptotic complexity to the previous regex approach).

### Completeness

- All 6 regression tests cover the identified edge cases:
  1. Sibling `*_id` columns remain `integer` (the original C1 bug)
  2. Quoted mixed-case identifiers (`"UserId"`)
  3. Last-column ownership (no trailing comma)
  4. Stranding guard (mis-filed DEFAULT prevents write)
  5. Full `Dump` entry point integration
  6. Column literally named `bigint` (type-token collision)
- 23 pre-existing tests continue to pass — no regressions.

### Observability

Not applicable for a CLI tool.

### Lifecycle & State Transitions

The merge post-processing is now unconditional and idempotent. No state leaks between the split
and merge phases.

## Findings

| Severity | Count | Details |
|----------|-------|---------|
| Critical | 0 | — |
| Warning | 0 | — |
| Suggestion | 0 | — |

## Gate Analysis

All 5 gates pass with linked evidence. See `evidence-manifest.md` for full details.

### Notable Observations

1. **C1 fix is surgically precise.** The structured scanner only touches the exact column definition
   line, leaving all surrounding SQL (including comments, constraints, and sibling columns)
   completely untouched. This is a significant improvement over the regex approach.

2. **Documentation update is empirical, not aspirational.** The README and Go doc comments now
   describe the actual observed behavior (which objects survive as separate files, which are
   inlined) rather than making absolute claims that could drift from implementation.

3. **N1 caveat is honestly documented.** The README now states that split output reflects
   creation order, not replay order — an inherent limitation of pg_dump output that the tool
   cannot fix. This is the right approach: document the limitation rather than pretend it
   doesn't exist.

## Release Engineer Note

**Impact:** The `split` and `dump` commands now always produce merged output. Downstream consumers
who relied on the type-partitioned `INDEX/`, `TRIGGER/`, `DEFAULT/`, `CONSTRAINT/`, `FK_CONSTRAINT/`
directories will see those directories no longer populated. The child objects are inlined into the
corresponding `TABLE/<table>.sql` file.

**Rollback:** Revert commits `2ffd3ab` and `5f8a2af`. By design there is no toggle to re-enable
the old behavior — the merge is unconditional.

**Release-please compatibility:** `version.txt` and `CHANGELOG.md` are untouched. Release-please
will derive the next version from conventional commit messages.

## Verdict

**All gates pass. No unresolved findings. Recommend DONE.**

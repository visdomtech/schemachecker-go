# Known Caveats

## N1 — Sequence ordering: `nextval` DEFAULT can precede its SEQUENCE in `index.txt`

When a sequence is **not** folded into `bigserial` (IDENTITY columns, sequences with
`CYCLE`/explicit `MINVALUE`/`MAXVALUE`, or sequences whose `OWNED BY` the parser cannot
attribute), the merge pass moves the `ALTER TABLE … ALTER COLUMN … SET DEFAULT nextval(…)`
statement from `DEFAULT/<table>.sql` into the owning `TABLE/<table>.sql`. That TABLE file
appears **earlier** in `index.txt` than the `SEQUENCE/<name>.sql` file it refers to.

**Effect:** Sequential replay of the split output (e.g. `psql -f index.txt`) fails with:

```
ERROR:  relation "<schema>_<table>_seq" does not exist
```

and the column silently loses its default — the `SET DEFAULT nextval(…)` statement is
rolled back with the transaction, leaving the column with no default at all.

**Root cause:** `index.txt` records creation order (the order `pg_dump` emitted the objects),
not dependency order. The in-repo consumer `merge.FromIndex` is unaffected because it splits
each file into statement blocks and does not rely on `index.txt` order for execution.

**Pre-existing defect** — unrelated to the bigserial matching fix. Not fixed in this release
because every remedy touches `index.txt` ordering or `internal/merge.FromIndex`, both of which
are out of scope for the merge-by-default change.

**Workaround:** Use `schemachecker merge` rather than `psql -f` over `index.txt`.

---

## W2 — Merge post-pass performance at scale

`MergeTableObjects` scans every INDEX, TRIGGER, DEFAULT, CONSTRAINT, and FK_CONSTRAINT file
for each TABLE, giving O(tables × child-files) complexity.

**Measured impact:**

| Tables | Child files | Merged (s) | Unmerged (s) | Ratio |
|--------|-------------|------------|---------------|-------|
| 300 | 900 | 4.48 | 0.28 | 16× |
| 1000 | 5000 | minutes | seconds | — |

**Root cause:** The merge loop at `internal/split/merge_table.go` re-reads and re-scans the
child directories for every table. A fix would require restructuring `MergeTableObjects` to
build a single index of child files upfront (e.g. by parsing the `-- Name:` / `-- Type:`
headers once and dispatching by owning table), which is a significant refactor.

**Not a correctness issue** — the output is identical regardless of performance. Not fixed
in this release because the refactor is out of scope.

**Workaround:** For very large schemas (>1000 tables), consider running `split` on a
per-schema basis if your database has multiple schemas, or accept the one-time cost
(split is typically run once per migration cycle, not in a hot loop).

# Iteration 2 Plan — split-merge-by-default

**Author:** System Architect (`multi-agent-planning`, local mode, read-only through the verdict).
**Input:** `docs/as-goals/split-merge-by-default/1/gap-summary.md` + `1/review.md` (iteration-1 gap
state, not the original goal). Re-synthesis mode: the iteration-1 plan is a supplied input.
**Repo state at authoring:** branch `main`, HEAD `46ca19c`, working tree clean apart from this file.
**Files written by this plan:** none. This document is the only artifact produced by the Architect.

**Constraints honored**

- The five `gates/*.md` files are treated as immutable. No gate is added, softened or reworded.
- W2 (`O(tables × index-files)` merge cost), S1 and S2 are **not** planned. §1.6 answers the one
  exception the Delivery Lead allowed: whether the C1 fix makes W2 materially worse. It does not.
- `e886939` is not amended. Its over-claim is corrected in a **follow-up commit** (T5).
- The goal's out-of-scope list is respected: no change to `internal/merge.FromIndex`, no change to
  `version.txt`/`CHANGELOG.md`, and no re-design of `MergeTableObjects` (the fix keeps its public
  signature, its phase order and its per-directory algorithms).

---

## 0. Method — three perspectives, run inline

| Perspective | What it wanted | Survived synthesis |
|---|---|---|
| **Simplicity** | Delete the regex interpolation entirely; parse the `CREATE TABLE` column list structurally. | Adopted. It is the only variant that fixes quoted identifiers without inventing a second regex dialect. |
| **Performance** | Do the minimum text edit; avoid any new directory read. | Partially adopted. A new per-schema `DEFAULT/` read is unavoidable for safety (§A.4); it is indexed by relation so its cost is measured at ≤1 % of the merge pass (§1.6). |
| **Minimal change** | Keep `inlineBigserial`/`removeDefaultNextval` as the units and only tighten their patterns. | Rejected — see F1. It fixes the symptom, leaves the quoted-identifier false negative in place, and would make the tool *silently* delete sequences whose `nextval` default it could not strip (§A.3). |

**Adversarial self-review** produced four findings that changed the plan: (a) fixing `inlineBigserial`
alone is *unsafe* — it newly enables quoted-column conversion, which `removeDefaultNextval` cannot
strip, leaving a dangling `nextval` reference to a deleted sequence (proved in §A.3); (b) the fix
needs an atomic write order so a failure cannot leave a half-inlined table (§A.4); (c) the
replacement offset must be computed from the parsed remainder, not searched, or a column literally
named `bigint` corrupts its own line (§A.2, test 6); (d) the always-merge pass has an ordering
defect independent of C1 (§1.5, N1) that the documentation must stop hiding.

---

## 1. Evidence base

Everything below was **executed** by this Architect, not argued. Reproduction recipe:

```sh
docker run -d --name sc-arch-i2-pg -e POSTGRES_PASSWORD=postgres postgres:17.10
# fixture loaded from /tmp/sc-arch-i2-w/setup.sql + setup2..9.sql (12 tables, 9 sequences,
# 8 nextval DEFAULT objects, 1 matview, 1 view + INSTEAD OF trigger, 1 foreign table + trigger,
# 1 partitioned table + child, 2 quoted relations with a space, IDENTITY, CYCLE, RLS policy,
# COMMENT, RULE, 3 FKs)
docker exec sc-arch-i2-pg pg_dump -U postgres --schema-only --no-owner sc > fresh.dump
go build -o /tmp/bin_head .            # at 46ca19c
/tmp/bin_head split fresh.dump out; /tmp/bin_head merge out/index.txt out.sql
docker exec sc-arch-i2-pg psql -U postgres -d <fresh db> -v ON_ERROR_STOP=0 -f /tmp/out.sql
```

### 1.1 C1 reproduced with the release binary on a real `pg_dump`

`fresh.dump` contains, verbatim (lines 137-142, 312):

```sql
CREATE TABLE public.foo (
    id bigint NOT NULL,
    external_id bigint NOT NULL,
    user_id bigint NOT NULL,
    width integer NOT NULL
);
...
ALTER TABLE ONLY public.foo ALTER COLUMN id SET DEFAULT nextval('public.foo_id_seq'::regclass);
```

Only `id` is owned by `public.foo_id_seq` (`ALTER SEQUENCE public.foo_id_seq OWNED BY public.foo.id`).

**HEAD binary** → `public/TABLE/foo.sql` after `split`:

```sql
CREATE TABLE public.foo (
    id bigserial NOT NULL,
    external_id bigserial NOT NULL,
    user_id bigserial NOT NULL,
    width integer NOT NULL
);
ALTER TABLE ONLY public.foo
    ADD CONSTRAINT foo_pkey PRIMARY KEY (id);
```

**Fixed implementation** → same file:

```sql
CREATE TABLE public.foo (
    id bigserial NOT NULL,
    external_id bigint NOT NULL,
    user_id bigint NOT NULL,
    width integer NOT NULL
);
ALTER TABLE ONLY public.foo
    ADD CONSTRAINT foo_pkey PRIMARY KEY (id);
```

### 1.2 C1 is material, not cosmetic

Replaying the two merged trees into fresh PostgreSQL 17 databases:

| | sequences in DB | sequence name set vs source `sc` |
|---|---|---|
| source `sc` | **9** | (reference) |
| HEAD `split`→`merge`→`psql -f` | **11** | **+ `foo_external_id_seq`, + `foo_user_id_seq`** |
| fixed `split`→`merge`→`psql -f` | **9** | **identical set** |

HEAD also gives those columns real defaults the source does not have:

```
HEAD  : external_id|bigint|nextval('foo_external_id_seq'::regclass)
        user_id    |bigint|nextval('foo_user_id_seq'::regclass)
fixed : external_id|bigint|           user_id|bigint|
```

At the iteration-1 scale fixture (300 tables) the HEAD binary corrupted **300/300** tables; the fixed
binary corrupts **0**.

### 1.3 Legitimate conversion still happens

The fix must not merely disable the inliner. Proved on the same dump:

- `public.mixed` — a `bigint NOT NULL` column next to three `DEFAULT …` columns — becomes
  `id bigserial NOT NULL`, its `SEQUENCE/mixed_id_seq.sql` and `DEFAULT/mixed.sql` are removed.
- `public."CaseTable"."UserId"` — **quoted, mixed case** — is a *newly* successful conversion
  (HEAD leaves it as `bigint NOT NULL` and keeps a stray `nextval` line in the TABLE file).

### 1.4 Sibling-column safety and the stranding guard, proved

`TestMergeInlineSequence*` at HEAD are 23/23 green over C1 because **no fixture has an `*_id`
sibling column**. All six new tests (§A.6) were run against the un-fixed `merge_table.go`
(`/tmp/mt_head.go` swapped back in): **all six FAIL**; with the fix: 29/29 pass, `go vet` clean,
`go test -race -count=1 ./...` green across all packages.

### 1.5 N1 — a second, independent defect found while proving the docs (must be documented)

For a sequence that is **not** inlined (IDENTITY, `CYCLE`/explicit `MINVALUE`/`MAXVALUE`, or a
relation the `OWNED BY` parser cannot attribute), `mergeByContent` moves
`ALTER TABLE … ALTER COLUMN … SET DEFAULT nextval(…)` out of `DEFAULT/<table>.sql` and into
`TABLE/<table>.sql`. In `index.txt` the `TABLE/` entry comes **before** the `SEQUENCE/` entry, so
sequential replay assigns a default from a sequence that does not exist yet:

```
v_fix/index.txt:18  public/TABLE/cycled.sql        <- holds the nextval ALTER after merging
v_fix/index.txt:19  public/SEQUENCE/cycled_id_seq.sql
psql: ERROR:  relation "public.cycled_id_seq" does not exist
```

Observed drift: source `cycled.id` = `nextval('cycled_id_seq'::regclass)`; replayed = **no default**.

This is **pre-existing at HEAD and orthogonal to C1** (HEAD reproduces it; the C1 fix removes exactly
one instance of it, `"CaseTable"`). `internal/merge` defers only `isFKBlock` statements
(`ADD CONSTRAINT` ∧ `FOREIGN KEY`), so `check`/`merge` inherit the same hazard. Fixing it properly
means dependency-aware index ordering or a `FromIndex` change — both named out of scope by the goal.
**Escalated in §G2; the only iteration-2 action is documentation (T3).**

Measured blast radius: 1 failing statement in the 12-table fixture, and it silently loses a column
default. Surviving `DEFAULT/` files that were *not* moved (e.g. `DEFAULT/Weird.sql`) are replay-safe
only because pg_dump's DEFAULT section follows its SEQUENCE section — an accident, not a guarantee.

### 1.6 Performance — the one question the Delivery Lead asked

Fixture: 300 tables × 900 indexes + 300 sequences + 300 `DEFAULT` objects (`big_seq.dump`, 289 KB).
Interleaved paired runs, median of wall-clock `split`:

| run | HEAD | C1 fix | fix + guard | paired median Δ vs HEAD |
|---|---|---|---|---|
| A (9 reps) | 10.06 s | 9.49 s | 10.70 s | fix −0.33 s · guard +0.49 s |
| B (7 reps) | 10.45 s | 9.94 s | 10.02 s (linear) / 10.62 s (indexed) | fix −0.60 s · guard −0.58 s / +0.08 s |

**Answer: the C1 fix does not make W2 materially worse; no escalation on that axis.** Two
independent paired runs disagree about sign, and the largest observed paired median delta is
+0.49 s on a 10.45 s baseline (≈5 %, smaller than the run-to-run spread). The asymptotic class is
unchanged: the guard reads `DEFAULT/` **once per schema** (not per table or per sequence) and indexes
the result by target relation, so it is `O(D)` per schema plus `O(1)` lookup per sequence. It becomes
quadratic only if a future edit moves `scanDefaultDir` inside the per-sequence loop — stated as a
prohibition in T2. W2 itself stays deferred and untouched.

---

## A. The C1 fix, designed precisely

### A.0 Root cause, restated as an invariant

`internal/split/merge_table.go:326-349` builds a pattern by interpolating a **database-derived
identifier** into regex source (`regexp.QuoteMeta(columnName)`) with no left boundary, no line
anchor, and then applies `ReplaceAllString` to the whole file (`:342`). The invariant that was
violated, and which the fix must restore:

> **INV-1.** A name that came from the database is never concatenated into a pattern. It is compared
> to a parsed token, with PostgreSQL identifier folding, at a position known to be a column
> definition.

`reCol` also has no notion of *which* occurrence to replace, so `ReplaceAllString` turns every
suffix match into a `bigserial`. Two defects, one site.

### A.1 Decision: parse the column list, do not regex it

**Decision: structured line scan of the `CREATE TABLE` body with a hand-written identifier reader.
No regex substitution, and no third-party SQL parser.**

Why not a better regex:

1. "Whole leading identifier of a column-definition line, optionally double-quoted, matched with PG
   folding semantics" is not expressible as a single Go regexp without alternation that still
   mishandles `""` escapes inside quoted identifiers. Any regex version keeps INV-1's violation live
   for the next reviewer.
2. The regex route fixes the *positive* match but leaves the *replacement* hazard: a `ReplaceAllString`
   over the file. Even with a perfect pattern, `"bigint" bigint NOT NULL`-style lines make
   "find the type token by searching the line" wrong (§A.2, step 6).
3. Regex substitution cannot be made atomic per column: the fix must be able to say "I found the
   column but I refuse to write" after checking the stranding guard (§A.4).

Why not a real SQL parser: `internal/pgdump` is a state machine over *statements*, not an expression
parser; adding pg_query/go (CGo) or a hand-written grammar would re-write `MergeTableObjects`, which
the goal excludes, and would put all 23 existing tests at risk for a defect that lives in 24 lines.

**Residual limitations of the chosen approach — accepted, and each one fails *safe*** (the sequence
keeps its `SEQUENCE/` file and the column keeps `bigint NOT NULL`; the tool never fabricates):

| # | Input shape | Behavior | Safe? |
|---|---|---|---|
| R1 | Single-line `CREATE TABLE t (id bigint NOT NULL, …);` | not converted | yes — pg_dump never emits this |
| R2 | Type token quoted (`id "bigint" NOT NULL`) | not converted | yes — invalid SQL anyway |
| R3 | Extra tokens after `NOT NULL` (`DEFAULT …`, `CHECK …`, trailing comment) | not converted | yes — conservative |
| R4 | Column order / `NOT NULL` written before the type | not converted | yes — not valid PG syntax |
| R5 | Only the **first** `CREATE TABLE` body in the file is scanned | later statements ignored | yes — `ResolveFilename` writes one table per file |
| R6 | `serialTypeTokens` unchanged: `{integer, int4, bigint, int8}`. `smallserial`/`serial`/`serial8` still not produced | same as HEAD | yes — deliberate; widening is out of scope (F4) |
| R7 | A second `ALTER TABLE … ALTER COLUMN <col> SET DEFAULT nextval(...)` in an unrelated file whose leading statement is not `ALTER TABLE` | stranding guard does not attribute it | **no** — residual. The guard already covers the TABLE file, the table's own `DEFAULT/` file, and every other `DEFAULT/` file of the schema whose leading `ALTER TABLE` targets the table; a `DEFAULT/` object is always a single `ALTER TABLE` statement in pg_dump output. Accepted and noted in T2's acceptance criteria. |

### A.2 The anchoring fix — normative algorithm

`bigserialColumn(content, columnName string) (string, bool)` replaces `inlineBigserial`'s body:

1. **Locate the body.** First line whose left-trimmed text case-insensitively starts with
   `CREATE TABLE`. `columnBodyEnd` = first subsequent line whose left-trimmed text starts with `)`.
   Scan only the lines strictly between them.
2. **Read the leading token** with `readIdentToken` (below). No match → skip the line.
3. **Compare with PostgreSQL folding** (`identEquals`): quoted → exact byte comparison (already
   de-escaped); unquoted → `strings.EqualFold`. This makes `"UserId"` match reference `UserId` and
   reject `userid`, while bare `ID` matches reference `id`. Never compare case-insensitively across
   the quoting boundary.
4. **Read the second token.** It must be unquoted and `strings.ToLower(name)` ∈ `serialTypeTokens`.
5. **Require the tail to be exactly the NOT NULL constraint**: `strings.Fields(tail)` is length 2,
   `fields[0]` ≈ `NOT`, `fields[1]` ≈ `NULL` or `NULL,`. (`NULL,` must be accepted: `strings.Fields`
   keeps the comma on the last column of a multi-column list — proven by a first attempt that failed
   `TestDumpMergesChildObjectsByDefault`.)
6. **Replace the type token at its computed offset — the only correct construction:**

   ```go
   typeStart := len(lines[i]) - len(strings.TrimLeft(rest, " \t"))
   lines[i] = lines[i][:typeStart] + "bigserial" + tail
   ```

   `rest` is a literal suffix of `lines[i]`, so this is exact. **`strings.Index(lines[i], typName)` is
   forbidden**: for `OWNED BY public.weird.bigint` and a line `    bigint bigint NOT NULL` it finds the
   *column* token and produces `    bigserial bigint NOT NULL` — verified red/green by test 6 (§A.6).
   At most one line is ever rewritten, and the function returns after it. There is no
   `ReplaceAllString` anywhere in the new code path.
7. Return `(joined, true)`, else `(content, false)`.

`readIdentToken(s) (name string, quoted bool, rest string, ok bool)`: trim `[ \t]`; if the next byte is
`"`, scan forward, converting `""` to a single `"` and returning the remainder after the closing quote;
otherwise scan `[A-Za-z0-9_$]+`. Unterminated quote → `ok = false` (skip the line). This is also the
reader reused by §A.3, so there is exactly one identifier grammar in the file.

**New shared constant, used by §A.3 and the guard:**

```go
const reQualifiedIdent = `(?:[\w$]+|"(?:[^"]|"")*")(?:\.(?:[\w$]+|"(?:[^"]|"")*"))*`
```

### A.3 The class, not just the bug — the mandatory pair fix

Tightening `reCol` alone is a **regression**. `reColumnIdent`-style quoted matching newly admits
columns like `"UserId"`, whose `ALTER TABLE ONLY public."CaseTable" ALTER COLUMN "UserId" SET DEFAULT
nextval('public."CaseTable_UserId_seq"'::regclass);` is invisible to the old stripper, whose class is
`[\w."]` with no space and whose column token is interpolated unquoted:

```go
`(?i)ALTER\s+TABLE\s+(?:ONLY\s+)?[\w."]+\s+ALTER\s+COLUMN\s+` + regexp.QuoteMeta(columnName) + …
```

Result at HEAD-with-only-the-anchor-fix: the column becomes `bigserial`, its `SEQUENCE/` file is
deleted, and the `nextval` line **survives in the TABLE file**. Measured with a parse-only variant
(`/tmp/bin_parsefix`, `m_pf.sql:158`):

```
ERROR:  relation "public.custom_seq_99" does not exist
```

i.e. the tool emits a default pointing at a sequence it removed. Therefore the two changes ship in
**one commit**:

- `stripDefaultNextval(content, columnName string) string` — quote-aware relation via
  `reQualifiedIdent`, quote-aware column via `reColumnIdent(columnName)` = `(?:<q>|"<q>")`,
  statement-terminated `nextval\([^)]*\)[^;]*;[ \t]*\n?`, and removal scoped to the matching
  statement. Pure over `content`; writing/removal stays with the caller (§A.4).
- Empty-file deletion stays the caller's job so no file is written before the guard runs.

### A.4 Atomicity + the stranding guard (required, not optional)

`inlineSequences` per sequence becomes **all-or-nothing**, in this order:

1. `parseSequenceOwnedBy` → `(table, column)`; `isStandardSequence` → else `slog.Debug`, `continue`.
2. Read `TABLE/<table>.sql`; absent → `slog.Warn` with `expected_path`, `continue`.
3. `bigserialColumn` → not converted → `slog.Warn("skipping sequence: owned column is not a plain " +
   "integer/bigint NOT NULL column definition", …)`, `continue`. (The current single
   `"table file not found"` warning covers both cases and hides C1's blast radius; it must be split.)
4. `newTable = stripDefaultNextval(newTable, column)`.
5. Read `DEFAULT/<table>.sql` if present → `newDefault = stripDefaultNextval(...)`.
6. **Guard.** `reLeftover := reSetDefaultColumn(column)` — deliberately **looser** than the stripper
   (`…ALTER TABLE <rel> ALTER COLUMN <col> SET DEFAULT nextval`), so a default the stripper missed is
   still detected. Stranded if `reLeftover` matches `newTable`, or `newDefault`, or the cached content
   of any **other** `DEFAULT/` file of this schema whose target relation equals `tableName`. Any
   strand → `slog.Warn("skipping sequence: a nextval default for the column could not be removed")`,
   `continue` **before any write**. The `SEQUENCE/` file then survives with its default, which is
   correct-but-unmerged, never dangling.
7. Only now: write `TABLE`, then write-or-remove `DEFAULT`, then remove `SEQUENCE`.

`scanDefaultDir(destDir, schemaName)` reads the schema's `DEFAULT/` files **once** and resolves each
one's relation with `reAlterTableTarget` (capture group **required**:
`(?im)^\s*ALTER\s+TABLE\s+(?:ONLY\s+)?(<reQualifiedIdent>)` — appending the constant without its own
group made `FindStringSubmatch` return a length-1 slice and panicked with
`index out of range [1] with length 1`). `indexByRelation` buckets those files by
`strings.ToLower(relation)`, which can only over-match, so the guard stays conservative in the safe
direction. After a DEFAULT file is rewritten or removed, its cached entry is updated so the guard
never reads a stale default it already stripped.

Proven by test 4: a `DEFAULT/id.sql` (column-keyed rather than table-keyed — exactly what a reversed
`-- Name: id users; Type: DEFAULT;` header produces) blocks the inline, and both files are preserved.

**Public API unchanged:** `MergeTableObjects(destDir string) error`, `Dump(dumpFile, destDir string) error`.
No exported symbol added or removed. `inlineBigserial` and `removeDefaultNextval` (the write-in-place
wrappers) are deleted; **no test calls them** — verified with
`grep -n "inlineBigserial\|removeDefaultNextval" internal/split/split_test.go` → empty, so the
restructuring is invisible to Gate 3's existing tests.

### A.5 Sibling sweep — every database-derived-identifier site in `internal/split/`

Class: **a name read from the dump is used as text (interpolated, split or compared) without
identifier boundaries or quoting.** Scope: `internal/split/` only. 13 sites examined.

| Function / var | file:line | Vulnerable? | Failure mode | On default path? | Verdict |
|---|---|---|---|---|---|
| `inlineBigserial` `reCol` | merge_table.go:337-338, `ReplaceAllString` :342 | **YES — false positive** | Fabricates `bigserial` on `external_id`, `user_id`, `workspace_id`; extra sequences on replay (§1.2) | **YES** (`split.go:80` → `MergeTableObjects:56`) | **FIX NOW** §A.2 |
| `removeDefaultNextval` `reCol` | merge_table.go:362-366 | **YES — false negative** | Cannot strip `"UserId"`/quoted-relation defaults; paired with the above it orphans a deleted sequence (§A.3, `m_pf.sql:158`) | **YES** | **FIX NOW** §A.3 — mandatory pair, one commit |
| `reOwnedBy` + `splitDotRef` + `parseSequenceOwnedBy` | merge_table.go:196, 289, 299 | **YES — false attribution** | `OWNED BY public."Weird Name".id` captures `public.` → table `public`, `TABLE/public.sql` not found → sequence survives. `strings.Split(ref,".")` also splits on a dot *inside* quotes (`public."a.b"`) | **YES** | **DEFER.** Safe today only because it under-matches. Enabling it would expand `bigserial` conversion to quoted relations *and* multiply N1 (§1.5) by moving more `DEFAULT` bodies into earlier `TABLE` files. Needs §G2 decided first, plus its own gate. |
| `reNameSplit` | split.go:27 (`^(?P<ref>.*?)\s(?P<name>.*)$`), used :218 | **YES — mis-naming** | Quoted relation with a space: `-- Name: Weird Name id; Type: DEFAULT;` → file `DEFAULT/Weird.sql`; `-- Name: Other Odd odd_foo_fk; Type: FK CONSTRAINT;` → `FK_CONSTRAINT/Other.sql`. Proved on a real dump (§B.1) | **YES** | **DEFER + DOCUMENT.** pg_dump's `-- Name:` header drops the quoting, so the token boundary is unrecoverable from the header; a fix requires re-deriving the name from the statement text. Root cause of two W1 survivors. |
| `reOnTable` / `referencesTable` / `mergeByContent` | merge_table.go:114, 150, 119-146 | **YES — false negatives** | `ON ONLY public.parts` captures `ONLY`; `ON public."Weird Name"` cannot match a class without a space; `ref == tableName` is exact-string. So matview indexes, partition-parent indexes, quoted-relation indexes and view triggers survive | **YES** | **DEFER + DOCUMENT** (W1). Same expansion hazard as `reOwnedBy`. Note `referencesTable` does whole-identifier comparison, so `users` cannot swallow `users_old` (iteration-1 review item 10) — the *symmetry* property is safe and must stay in the regression set. |
| `reSeqNonStandard` | merge_table.go:200 | no | Line-anchored `(?im)^\s*(CYCLE\|MINVALUE\s+-?\d\|MAXVALUE\s+-?\d)`; interpolates nothing | yes | Keep. **This is the model to copy.** Its `NO CYCLE`/`NO MINVALUE` avoidance is why the anchor must be `^\s*`, not `\b`. |
| `appendIfExists` | merge_table.go:161 | no | Exact filename equality (`<tableName>.sql`); no identifier parsing | yes | Keep. Filename-keyed merging is the reason `DEFAULT/` and `FK_CONSTRAINT/` survive only in the mis-named cases. |
| `mergeableTypes` / `contentMergeTypes` loops | merge_table.go:15-21, 94-105 | no | Static directory names; `CONSTRAINT` is a dead entry (`ResolveFilename` never creates it) | yes | Keep. Removing the dead `CONSTRAINT` entry is optional cleanup, explicitly **not** in scope (it would change `split`'s pre-merge behavior). |
| `updateIndexAfterMerge` | merge_table.go:425 | no | Stat-based pruning of `index.txt`; no identifier use | yes | Keep. Verified `orphaned` exit 0 on the fixed tree (§D Gate 1). |
| `reObjDesc` | split.go:20, used :191 | no | Parses the fixed `-- Name: X; Type: Y; Schema: Z; Owner: -` frame; semicolon-delimited, so a space in the name is not a boundary error *for the frame* | yes | Keep. |
| `reFunctionName` | split.go:28, used :226 | latent | Non-greedy `\((?P<parameters>.*?)\)` truncates at the first `)` inside a signature | yes | **OUT OF CLASS.** Produces a filename, never an identifier comparison. Not touched. |
| `reSeqSet` / `reSeqSetDefault` | split.go:25-26, used :139 | latent | `(?P<name>.*?)` up to the first `'`; quoted sequence names are mis-keyed | yes (`SEQUENCE_SET/`) | **DEFER.** Same quoting family as `reOwnedBy`; belongs to the §G2 decision, not to C1. |
| `removeEmptyDirs` / `readLines` | merge_table.go:377, 404 | no | Path/basename and file-reading operations only | yes | Keep. |

**Path-escape check (Security):** `reOwnedBy`'s character class `[\w."]` contains no `/`, and
`stripQuotes` does not introduce one, so `tableName` cannot contain a separator. `TABLE/<tableName>.sql`
therefore stays inside `destDir`. The C1 fix adds no new path construction. **No security finding.**

**Verdict: 6 vulnerable sites, all 6 on the default path. 2 fixed in iteration 2 (they are one
atomic change), 4 deferred with documented behavior.**

### A.6 Regression suite — six new tests, all red at HEAD

All in `internal/split/split_test.go`. New helpers `mustMkdirAll`, `mustWrite`, `mustRead` (the file
currently has only `readFile(t, root, rel…)` at :1269). No existing test is modified.

| # | Test | Fixture | Assertions | Catches |
|---|---|---|---|---|
| 1 | `TestMergeInlineSequenceSiblingIDColumnsUnchanged` | `TABLE/foo.sql` = `id bigint NOT NULL, external_id bigint NOT NULL, user_id bigint NOT NULL, workspace_id integer NOT NULL, width integer NOT NULL`; `SEQUENCE/foo_id_seq.sql` with `OWNED BY public.foo.id`; `DEFAULT/foo.sql` with the `id` nextval | **exact whole-file string equality** with `id bigserial NOT NULL` and all siblings byte-identical; `strings.Count(got,"bigserial") == 1`; `SEQUENCE/foo_id_seq.sql` and `DEFAULT/foo.sql` are `os.IsNotExist` | C1 core, plus that the inline still happened and still cleaned up |
| 2 | `TestMergeInlineSequenceQuotedColumnIdentifier` | `TABLE/CaseTable.sql` = `"UserId" bigint NOT NULL, "OtherId" bigint NOT NULL, id bigint NOT NULL`; `SEQUENCE/CaseTable_UserId_seq.sql` `OWNED BY public."CaseTable"."UserId"`; `DEFAULT/CaseTable.sql` quoted nextval | contains `"UserId" bigserial NOT NULL`; **not** `"OtherId" bigserial`; **not** `    id bigserial`; **no `nextval` anywhere in the file**; both child files removed | the A.3 pair fix; quoted folding; that `id` (a *shorter* name) is not swallowed by the quoted column |
| 3 | `TestMergeInlineSequenceOwnedColumnLast` | `TABLE/lastcol.sql` = `user_id bigint NOT NULL, id bigint NOT NULL` (owner is last, no trailing comma) | exact whole-file equality `user_id bigint NOT NULL,\n    id bigserial NOT NULL` | the `NULL,` token handling (R5) and the no-comma last-column case |
| 4 | `TestMergeInlineSequenceDoesNotStrandSurvivingDefault` | `TABLE/keepme.sql` = `id bigint NOT NULL, val text`; standard `SEQUENCE/keepme_id_seq.sql` `OWNED BY public.keepme.id`; **mis-filed `DEFAULT/id.sql`** (column-keyed) holding the nextval ALTER | TABLE file contains **no** `bigserial`; `SEQUENCE/keepme_id_seq.sql` and `DEFAULT/id.sql` both still exist | the §A.4 guard; proves the guard is directory-aware, not file-local |
| 5 | `TestDumpDefaultPathDoesNotFabricateBigserial` | full `Dump` over a synthetic dump: `orders(id bigint NOT NULL, user_id bigint NOT NULL, total numeric(10,2))` + `orders_id_seq` + `OWNED BY orders.id` + `-- Name: orders id; Type: DEFAULT;` | `public/TABLE/orders.sql` does **not** contain `user_id bigserial` and **does** contain `id bigserial NOT NULL` | the real entry point (Gate 1's default path), not just the unit |
| 6 | `TestMergeInlineSequenceColumnNamedLikeTypeToken` | `TABLE/weird.sql` = `id bigint NOT NULL, bigint bigint NOT NULL`; `OWNED BY public.weird.bigint`; `DEFAULT/weird.sql` `ALTER COLUMN bigint` | exact whole-file equality `    id bigint NOT NULL,\n    bigint bigserial NOT NULL`; and the result must **not** contain `bigserial bigint` | the A.2 step-6 offset rule. Verified red with `strings.Index`, green with the suffix-arithmetic |

Test 2 doubles as the "legitimately-convertible column still converts" case in the required shape, and
§1.3 proves the same end-to-end on a real dump (`mixed`, `"CaseTable"`).

**Red/green protocol (part of T1's acceptance):** run
`go test -count=1 -run 'TestMergeInlineSequence|TestDumpDefaultPath' ./internal/split/` with
`git show HEAD:internal/split/merge_table.go` restored over the scratch copy. Expected: tests 1-6 all
FAIL, the 23 pre-existing tests all PASS. Record the verbatim output in `2/evidence-manifest.md`.

---

## B. Gate 4 / W1 doc correction, based on evidence

### B.1 The empirical merge/survive table

Produced with the fixed binary over `fresh.dump` (real `pg_dump 17.10`, `--schema-only --no-owner`)
and `fk.dump` (same fixture plus two FKs on quoted relations). **This is the table the Documentation
Engineer writes; every row was observed, none inferred.**

| Object category (pg_dump `Type:`) | Outcome | Observed file / evidence | Why |
|---|---|---|---|
| `TABLE` | survives (it is the target) | `public/TABLE/foo.sql` | — |
| `INDEX` on a table | **MERGED** | `users_name_idx`, `foo_id_idx` → `TABLE/*.sql`, `INDEX/` removed | filename equals table |
| `INDEX` on a partition **child** | **MERGED** | `parts_2024_id_idx` → `TABLE/parts_2024.sql` | child is its own TABLE file |
| `INDEX` on a **materialized view** | **survives** | `public/INDEX/mv_id_idx.sql` | `mv` has no TABLE file |
| `INDEX` on a **partitioned parent** (`ON ONLY public.parts`) | **survives** | `public/INDEX/parts_id_idx.sql` | `reOnTable` captures `ONLY` |
| `INDEX` on a relation needing quoting with a space | **survives** | `public/INDEX/weird_name_idx.sql` (`ON public."Weird Name"`) | `[\w.]+` cannot match a space |
| `INDEX` on a **foreign table** | n/a | not constructible: PostgreSQL 17 rejects `CREATE INDEX … ON public.ft` | observed `ERROR` |
| `TRIGGER` on a table | **MERGED** | into `TABLE/*.sql`, `TRIGGER/` removed | content `ON <table>` matches |
| `INSTEAD OF` trigger on a **view** | **survives** | `public/TRIGGER/v.sql` | `v` is a VIEW, no TABLE file |
| trigger on a **foreign table** | **survives** | `public/TRIGGER/ft.sql` | same reason |
| `DEFAULT` (nextval) whose sequence is inlined | **MERGED, then removed** | `DEFAULT/foo.sql` deleted | `stripDefaultNextval` + `appendIfExists` |
| `DEFAULT` whose sequence **survives** (CYCLE / non-attributable) | **MERGED** into the earlier TABLE file → **N1 replay break** | `DEFAULT/cycled.sql` → `TABLE/cycled.sql` (index 18 vs 19) | `appendIfExists` |
| `DEFAULT` on a quoted relation with a space | **survives, mis-named** | `public/DEFAULT/Weird.sql`, `public/DEFAULT/Other.sql` | `reNameSplit` §A.5 |
| `CONSTRAINT` (PK/UNIQUE) | **never emitted at all** | no `CONSTRAINT/` dir in HEAD *or* `55d5169` output | `ResolveFilename` appends straight into the TABLE file (split.go:258-259) |
| `FK CONSTRAINT` on a plain table | **MERGED** | `users users_foo_fk` → `TABLE/users.sql` | filename equals table |
| `FK CONSTRAINT` on a quoted relation with a space | **survives, mis-named** | `public/FK_CONSTRAINT/Other.sql`, `…/Weird.sql` (from `fk.dump`) | `reNameSplit`; replay-safe anyway because `merge.FromIndex` defers every FK block |
| `SEQUENCE`, standard `OWNED BY`, unquoted | **REMOVED** (folded into `bigserial`) | `foo_id_seq`, `mixed_id_seq`, `lastcol_id_seq`, `users_id_seq` | `inlineSequences` |
| `SEQUENCE`, standard `OWNED BY`, **quoted mixed-case column** | **REMOVED** — *new in iteration 2* | `CaseTable_UserId_seq` (HEAD keeps it) | §A.3 |
| `SEQUENCE`, `IDENTITY` column | **survives** | `public/SEQUENCE/users_ident_seq.sql` | no `OWNED BY` to parse |
| `SEQUENCE` with non-standard options (`CYCLE`, explicit `MINVALUE`/`MAXVALUE`) | **survives** | `public/SEQUENCE/cycled_id_seq.sql` | `reSeqNonStandard` |
| `SEQUENCE` with `OWNED BY` on a quoted relation with a space | **survives** | `public/SEQUENCE/Weird Name_id_seq.sql`, `custom_seq_99.sql` | `reOwnedBy` mis-attributes table `public` (warning `expected_path=…/TABLE/public.sql`) |
| `SEQUENCE` owned by a **`smallint`** column | **survives** | no `smallserial` in `serialTypeTokens` | R6 |
| `POLICY` | survives | `public/POLICY/users.sql` — named after the **table**, not the policy | split.go:264-265 |
| `ROW SECURITY` | survives | `public/ROW SECURITY/users.sql` | not in either merge list |
| `COMMENT` | survives | `public/COMMENT/foo.sql`, `…/foo.width.sql` | not in either merge list |
| `RULE` | survives | `public/RULE/v v_r.sql` | not in either merge list |
| `MATERIALIZED VIEW` / `VIEW` / `FOREIGN TABLE` | survive | `public/MATERIALIZED VIEW/mv.sql` etc. | no TABLE file |
| `FUNCTION` / `EXTENSION` / `SCHEMAS` / `TABLE DATA` / `SEQUENCE DATA(setval)` / `TABLE ATTACH` / `INDEX ATTACH` | survive | `public/TABLE ATTACH/parts_2024.sql`, `public/INDEX ATTACH/parts_2024_id_idx.sql`, … | not in either merge list |
| non-`nextval` column defaults (`(1+1)`, function call, constant, `now()`) | **stay inline in `CREATE TABLE`** — pg_dump emits **no** `Type: DEFAULT` object for them | `defprobe.dump`; all 8 `DEFAULT` objects in the fixture dump are `nextval` | verified by probe dump |

Deterministic and orphan-free after the fix: two independent `split` runs over `fresh.dump` →
`dirdiff` exit 0 with no Added/Removed/Changed; `orphaned index.txt` exit 0.

### B.2 `README.md` — exact replacements (all line numbers at HEAD `46ca19c`)

**(a) Lines 153-156 — the over-claim. Replace the bullet wholesale:**

> - There are **no** `INDEX/`, `TRIGGER/`, `DEFAULT/` or `FK_CONSTRAINT/`
>   directories — that SQL is inside `TABLE/<table>.sql`. `CONSTRAINT/` is never
>   emitted either: primary-key and unique constraints are appended straight into the
>   TABLE file while splitting, before any merge runs.

with

> - `INDEX/`, `TRIGGER/`, `DEFAULT/` and `FK_CONSTRAINT/` normally disappear, because
>   those child objects are appended into `TABLE/<table>.sql`. It is **not** a
>   guarantee. The merge finds a child's owner either by filename (default,
>   constraint, foreign key) or by the `ON <relation>` clause in the SQL (index,
>   trigger), so an object stays in its own directory when neither can resolve to a
>   table file. Observed survivors with real `pg_dump 17` output: indexes on
>   materialized views, indexes on a partitioned parent (`ON ONLY …`), indexes whose
>   relation needs double-quoting and contains a space, `INSTEAD OF` triggers on
>   views, triggers on foreign tables, and `DEFAULT`/`FK_CONSTRAINT` files for such
>   quoted relations — those files are additionally named after the first word of
>   the relation (`DEFAULT/Weird.sql` for `public."Weird Name"`).
>   `CONSTRAINT/` really is never emitted: primary-key and unique constraints are
>   appended straight into the TABLE file while splitting, before any merge runs.

**(b) Lines 157-160 — the `SEQUENCE/` bullet. Replace:**

> - `SEQUENCE/` survives for `IDENTITY` columns and for sequences with non-standard
>   options. A plain `OWNED BY` sequence on an integer/bigint column is folded into
>   the column as `bigserial` and its sequence file is removed, which is why `public`
>   above has no `SEQUENCE/` directory while `orca` does.

with

> - `SEQUENCE/` survives for `IDENTITY` columns, for sequences with non-standard
>   options (`CYCLE`, an explicit `MINVALUE` or `MAXVALUE`), for sequences owned by a
>   column that is not a plain `integer`/`bigint NOT NULL` (including `smallint` —
>   `smallserial` is never generated), for sequences whose `OWNED BY` relation needs
>   double-quoting, and whenever the `nextval` default cannot be removed from every
>   file that holds it. A plain `OWNED BY` sequence on an `integer`/`bigint
>   NOT NULL` column is folded into the column as `bigserial`, its `DEFAULT`
>   assignment is deleted and its sequence file is removed. Only the column named in
>   `OWNED BY` is rewritten — sibling columns such as `user_id` or `external_id` keep
>   their declared type, and a quoted mixed-case column (`"UserId"`) is matched
>   exactly, not case-insensitively.

**(c) Lines 107-115 — "a creation order, not a replay order". The paragraph currently names only FK
constraints. Append this sentence after "…which is what `check` and the `merge` subcommand use.":**

> A second, known case: when a sequence is **not** folded into `bigserial`, merging
> moves its `ALTER TABLE … ALTER COLUMN … SET DEFAULT nextval(…)` from `DEFAULT/` into
> the owning `TABLE/<table>.sql`, which appears **earlier** in `index.txt` than the
> `SEQUENCE/` file it refers to. Sequential replay then fails with
> `ERROR: relation "<schema>_<table>_seq" does not exist` and the column silently
> loses its default. This affects `IDENTITY`-adjacent and non-standard
> (`CYCLE`/`MINVALUE`/`MAXVALUE`) sequences, is a pre-existing defect unrelated to
> the bigserial matching fix, and is **not** fixed in this release — see
> `docs/as-goals/split-merge-by-default/1/review.md` §7 row 3 and
> `docs/as-goals/split-merge-by-default/2/plan.md` §1.5. Use `schemachecker merge`
> rather than `psql -f` over `index.txt`.

**(d) Lines 161-163 — extend the survivor list.** Current text lists
`POLICY/, FUNCTION/, VIEW/, DATA/, EXTENSION/` and `SCHEMAS/`. The observed set is larger. Replace with:

> - `POLICY/`, `FUNCTION/`, `VIEW/`, `MATERIALIZED VIEW/`, `FOREIGN TABLE/`, `COMMENT/`,
>   `RULE/`, `ROW SECURITY/`, `TABLE ATTACH/`, `INDEX ATTACH/`, `DATA/`, `EXTENSION/`
>   and `SCHEMAS/` keep their own files; `POLICY/` files are named after the table
>   they protect (`users.sql`), not after the policy.

**(e) Line 93-96 (`split` section intro) and line 91.** The phrase "Table-owned child objects are
always inlined" is correct **only** with "table-owned" visible, so keep it — but the following
bullet (a) must not contradict it. Require the Documentation Engineer to check that "always inlined"
and the new "(a)" bullet appear in the same section, and to leave lines 91-96 otherwise unchanged.

**(f) The example tree at lines 98-152.** It currently has no `SEQUENCE/` under `public` yet the
prose claims `orca` does. After (a)-(e), re-verify the tree block against a fresh run of
`find <outputDir> | sort` on the fixture used for Gate 1 and update only if it drifts; do not hand
edit it to look nicer.

### B.3 Go doc comments — exact replacements

**`cmd/split.go:10-18`** — current: "…Objects that are not table-owned keep their own files: POLICY,
FUNCTION, VIEW, DATA, EXTENSION, SCHEMAS and SEQUENCE_SET." Replace that sentence with:

```go
// Objects that are not table-owned keep their own files: POLICY, ROW SECURITY,
// COMMENT, RULE, FUNCTION, VIEW, MATERIALIZED VIEW, FOREIGN TABLE, TABLE ATTACH,
// INDEX ATTACH, DATA, EXTENSION, SCHEMAS and SEQUENCE_SET. Table-owned children
// normally merge away, but an INDEX or TRIGGER whose ON clause does not resolve to
// a table file (materialized views, partitioned parents via ON ONLY, relations
// whose name needs quoting) and a DEFAULT or FK CONSTRAINT filed under a quoted
// relation name stay in their own directory. A SEQUENCE keeps its own file unless
// it is a standard OWNED BY sequence on a plain integer/bigint NOT NULL column,
// which is inlined as a bigserial column type (IDENTITY sequences, sequences with
// CYCLE / explicit MINVALUE / MAXVALUE, and sequences whose owning nextval default
// could not be removed from every file holding it are always preserved).
```

**`cmd/dump.go:17-20`** — after "…is inlined into the owning `<schema>/TABLE/<table>.sql`." add:
`// See RunSplit for the child objects that legitimately survive as separate files.`
(Gate 2 also requires this comment to not describe a "no merge" mode — it already does not.)

**`internal/split/split.go:4-7` (package doc)** — change
"table-owned INDEX, TRIGGER, DEFAULT and FK_CONSTRAINT SQL is inlined into the owning
`<schema>/TABLE/<table>.sql` and the per-type child files are removed." to add
`, except for children whose owning table cannot be resolved from the file name or the `ON` clause`
and keep the last sentence ("The merged tree is therefore the only output shape this package
produces.") — Gate 4 requires the merge to be described as unconditional, which stays true.

**`internal/split/split.go:43-45` (`Dump` doc)** — same qualifier as the package doc; do not weaken
"unconditionally".

**`internal/split/merge_table.go` `MergeTableObjects` doc (lines 23-29)** — replace with a comment that
states the two resolution strategies (filename for `DEFAULT`/`CONSTRAINT`/`FK_CONSTRAINT`, content for
`INDEX`/`TRIGGER`), names the survivor categories, and keeps the existing NOT-idempotent warning
verbatim. Owned by **T2**, not T3, because it is in the file T2 already edits.

**`inlineSequences` doc** — state INV-1 in one line: the owned column is matched as a parsed
identifier on its own definition line, and only that one type token is rewritten.

### B.4 Correcting the record of `e886939` (T5, follow-up commit — never an amend)

The commit body says: "`public/INDEX/, public/TRIGGER/, public/DEFAULT/ and public/FK_CONSTRAINT/ are
no longer emitted`" and "POLICY/, SEQUENCE/, FUNCTION/, VIEW/, DATA/, EXTENSION/, SCHEMAS/ are
unchanged". Both are over-broad per §B.1. It also omits C1. T5's message body is normative:

```
docs(split): correct the 1.0.2 record for surviving child objects and C1

Supersedes two claims in the body of e886939 (feat(split): always merge
table-owned objects into TABLE files), which e886939 is not rewritten to carry.

1. "public/INDEX/, public/TRIGGER/, public/DEFAULT/ and public/FK_CONSTRAINT/
   are no longer emitted" is too broad. They disappear only when the merge can
   resolve a child's owning table. Measured survivors on real pg_dump 17.10
   output: indexes on materialized views, indexes on a partitioned parent
   (ON ONLY), indexes on a relation whose name needs double quotes and contains
   a space, INSTEAD OF triggers on views, triggers on foreign tables, and
   DEFAULT / FK_CONSTRAINT files for such quoted relations. CONSTRAINT/ is
   genuinely never emitted.
2. The commit did not disclose a behavior regression present in it: because the
   column matcher interpolated the OWNED BY column name without boundaries and
   applied ReplaceAllString to the whole file, always-merge rewrote every column
   whose name ENDS WITH the owned column (external_id, user_id, workspace_id) to
   bigserial, and replaying the merged tree created sequences the source schema
   never had. Fixed in the preceding commit
   (<T2 subject>); iteration-1 review recorded it as C1.
3. Also disclosed here and not fixed: merging a non-inlined sequence's nextval
   DEFAULT into the earlier TABLE file breaks sequential psql -f replay with
   "relation <seq> does not exist" and silently drops the column default.
   Pre-existing, independent of the C1 fix, tracked as N1.

version.txt and CHANGELOG.md remain release-please-owned and are untouched.
```

---

## C. Task breakdown

**WIP limit: one Engineer task at a time.** T1 → T2 → T3 → T4 → T5. No task touches more than five
files. T1 and T2 land as one behavior commit (they must — §A.3 proves the anchor fix alone is unsafe);
T3 as a docs commit; T5 as the record commit.

### T1 — C1 regression tests, red (Engineer)

- **Files (1):** `internal/split/split_test.go`
- Add helpers `mustMkdirAll`/`mustWrite`/`mustRead`; add the six tests of §A.6 verbatim in intent.
  Modify no existing test. Do not run `gofmt -w` over the whole file — it has two pre-existing
  alignment hunks (`@@ -404,7`, `@@ -917,8`) that must stay so the commit carries no unrelated
  reformatting. Verify with `gofmt -d internal/split/split_test.go` that the output is still exactly
  those two hunks.
- **Acceptance:** `go test -count=1 -run 'TestMergeInlineSequence|TestDumpDefaultPath' ./internal/split/`
  fails on all six new tests **and** passes all 23 pre-existing tests; capture the verbatim failure
  output for `2/evidence-manifest.md`.
- **Serves:** Gate 3 (the tests must exist to be green later) and the gap-summary requirement that
  unit tests cover the `foo(id, external_id, user_id, width)` shape that 23/23 missed.

### T2 — The fix, green (Engineer)

- **Files (1):** `internal/split/merge_table.go`
- Implement §A.2 (`bigserialColumn` + `readIdentToken`/`identEquals`/`isNotNullConstraint`/
  `columnBodyEnd`/`isCreateTableHeader`/`serialTypeTokens`/`reColumnIdent`/`reQualifiedIdent`),
  §A.3 (`stripDefaultNextval`, `reSetDefaultColumn`) and §A.4 (`scanDefaultDir`,
  `indexByRelation`, atomic `inlineSequences`); delete `inlineBigserial` and `removeDefaultNextval`;
  split the two conflated warnings; update the `MergeTableObjects` and `inlineSequences` doc comments
  per §B.3.
- **Prohibitions:** no change to `MergeTableObjects`' signature or phase order; no change to
  `serialTypeTokens` membership; no change to `mergeByContent`, `referencesTable`, `reOnTable`,
  `reOwnedBy`, `splitDotRef`, `parseSequenceOwnedBy`, `appendIfExists`, `updateIndexAfterMerge`,
  `split.go`, `cmd/*`; `scanDefaultDir` stays **outside** the per-sequence loop; no
  `ReplaceAllString` in the inline path; no new dependency.
- **Acceptance:** (1) T1's six tests pass, 29/29 green; (2) `go build ./... && go vet ./... &&
  go test -race -count=1 ./...` all green; (3) real-dump reproduction per §1.1-§1.3 with the built
  binary: `foo.sql` byte-matches §1.1's "fixed" block, merged-tree replay yields exactly the source's
  9-sequence set, `mixed` and `"CaseTable"` still convert, `orphaned index.txt` exit 0, two runs
  `dirdiff` exit 0; (4) `gofmt -l internal/split/merge_table.go` empty (its two pre-existing stray
  blank lines near `:199`/`:315` are incidentally cleaned — acceptable, `task ci` does not run gofmt).
- **Serves:** Gate 1, Gate 3, and closes C1.

### T3 — Documentation, evidence-first (Documentation Engineer)

- **Files (4):** `README.md`, `cmd/split.go`, `cmd/dump.go`, `internal/split/split.go`
- Apply §B.2 (a)-(f), §B.3 and the §B.1 table. Every claim written must be traceable to a §B.1 row;
  if the Documentation Engineer re-runs the split and observes a different survivor set, they record
  the observation and write **that**, not the plan's guess.
- **Acceptance:** Gate 4's grep proofs hold (no `--merge`, no "no merge", no "raw pg_dump
  representation" in `README.md`/`AGENTS.md`/Go doc comments; the package and `Dump` comments still
  describe the merge as **unconditional**); the README bullet no longer contains "There are **no**
  `INDEX/`"; the README `split` section's survivor list matches the Gate 1 smoke tree and the §B.1
  table; the N1 caveat sentence from §B.2(c) is present.
- **Serves:** Gate 4 (the failing gate), W1, and the documentation half of N1.

### T4 — Re-produce all five gates (Test Engineer)

- **Files (1, new):** `docs/as-goals/split-merge-by-default/2/evidence-manifest.md`
- Personal re-production in the order of §D. No Engineer self-report accepted.
- **Acceptance:** each of the five gates carries a Pass/Fail verdict with linked, self-produced
  evidence, per `gates/*.md` "Evidence Required" bullets.

### T5 — Commits and record correction (Release Engineer, with Delivery Lead)

- **Files:** the three commits; `docs/as-goals/**` bookkeeping only for T5.
- **Acceptance:** commit 1 = T1+T2 (behavior), commit 2 = T3 (docs), commit 3 = T5's message from
  §B.4 verbatim, touching only `docs/as-goals/**`. `e886939` is **not** amended, rebased or
  re-signed. `git diff --stat <base>..HEAD -- version.txt CHANGELOG.md` empty; `git status` clean.
- **Serves:** Gate 5.

---

## D. Gate risk analysis — regression risk from the C1 fix

### Gate 1 (merge-always) — **elevated, must be re-earned; it will still pass**

The fix changes what `MergeTableObjects` writes into `TABLE/*.sql`, so Gate 1's "child SQL present in
`TABLE/<table>.sql`" assertion is directly at risk. It passes because the inline path is unchanged for
table-owned children whose owner resolves; only the *column-level* text differs.
**Evidence to re-produce:** grep proof (`grep -rn "Options" internal/split` → empty;
`Dump(dumpFile, destDir string) error` with an unconditional `MergeTableObjects(destDir)` at
split.go:80); the CLI smoke run on the gate's **own** synthetic dump (TABLE + INDEX + TRIGGER +
CONSTRAINT + DEFAULT, all table-owned) showing all four child directories gone and no orphan
`index.txt` entry (`orphaned` exit 0); `TestDumpMergesChildObjectsByDefault` still green; plus a
second `split` of the same real dump compared with `dirdiff` (exit 0).
**Do not** widen Gate 1's synthetic fixture with a matview index, an `INSTEAD OF` trigger or a quoted
relation — the gate's own Verification Method fixes the fixture, and such objects legitimately survive
(§B.1), so adding them would test a claim the gate does not make. Put them in the Gate 4 evidence
instead.

### Gate 2 (call-sites-migrated) — **negligible**

T2 touches one file and adds/changes no exported symbol (`grep -rn "split.Dump" --include='*.go' .`
still 6 two-argument sites; `grep -rn "split.Options" .` still empty; `go build ./...` exit 0).
`cmd/dump.go`'s "no merge" phrasing is already absent; T3 only appends a cross-reference. Evidence:
the three greps plus the build.

### Gate 3 (suite-green) — **highest exposure**

The fix deletes two functions and changes the exact-file assertions of existing tests.
**Evidence to re-produce, verbatim:** `go build ./...`, `go vet ./...`,
`go test -race -count=1 ./...` with no `FAIL`; named lines for
`TestMergePreservesIdentitySequence` and `TestMergeInlineIdentitySequence` (the IDENTITY guarantee is
untouched — `inlineSequences` still `continue`s before the rewrite whenever `OWNED BY` is absent, and
`serialTypeTokens` is unchanged); the six new tests green; and the red run from T1 preserved as proof
the new tests are not vacuous. Watch specifically for `TestMergeTableObjects` (starts at
split_test.go:381; its `DEFAULT/orders.sql` fixture is
`ALTER TABLE public.orders ALTER COLUMN id SET DEFAULT nextval('orders_id_seq');` — a schema-**less**
`nextval` argument with **no** `::regclass` cast, which `stripDefaultNextval`'s
`nextval\([^)]*\)[^;]*;` must keep accepting), for `TestMergeInlineSequence` (starts at :599, uses
`nextval('users_id_seq'::regclass)`), and for `TestMergeInlineSequenceNoCycleIsStandard` /
`TestMergeInlineSequenceNegativeMinvalue` (they pin `reSeqNonStandard`, which T2 must not touch).
`TestMergeTableObjects` is also the single existing fixture with an `*_id` sibling column, and its
`user_id` is declared plain `integer` **without** `NOT NULL` — so even HEAD's unanchored pattern did
not match it. That is the complete explanation of why 23/23 tests were green over C1, and why T1's
sibling columns must be `bigint NOT NULL` / `integer NOT NULL`.

### Gate 4 (docs-consistent) — **the target of the iteration**

Risk is that the correction overshoots and starts describing the merge as conditional, which would
break Gate 4's own "describes merged output as the behavior rather than as an option" condition.
**Evidence:** the grep proofs, plus reading the README `split`/`dump` sections against the Gate 1
smoke tree **and** the §B.1 table, plus confirming the three qualifiers added to `split.go`'s package
and `Dump` comments still say *unconditional*.

### Gate 5 (release-impact-documented) — **at risk from the plan, not from the code**

Iteration 1 passed "mechanical, with a Warning"; its Condition text itself asserts the over-broad claim
("the type-partitioned `INDEX/`, `TRIGGER/`, `DEFAULT/`, `CONSTRAINT/`, `FK_CONSTRAINT/` directories
are no longer emitted"). The gates are immutable, so this must be satisfied by the **commit message**
carrying the claim **plus** its measured exceptions, not by rewording the gate. T5's message (§B.4)
does exactly that. **Evidence:** `git log -1 --format=%B` verbatim in the manifest;
`git show --stat` for each of the three commits; `git diff --stat <base>..HEAD -- version.txt
CHANGELOG.md` empty; `git status` clean; rollback = "revert the behavior commit" (T2's commit is the
only behavioral one, and it changes no API).

---

## E. Risks and mitigations

| Risk | Likelihood | Mitigation |
|---|---|---|
| Replacing regex with a line scanner silently stops converting a dump shape the regex handled | medium | R1-R6 table (§A.1) enumerates each shape and its fail-safe direction; T1 test 1 asserts the *positive* conversion, `TestMergeInlineSequence` and `TestDumpMergesChildObjectsByDefault` pin the existing ones; §1.3 proves `mixed` and `"CaseTable"` on a real dump |
| The stranding guard over-aborts, so sequences stop folding and more `SEQUENCE/` files appear | low | The guard's `reLeftover` is column-scoped, not table-scoped; §1.4/§D-3 require `TestMergeInlineSequence` green and the real-dump 9-sequence count; a survivor is always safer than a dangling `nextval` |
| New quoted-identifier conversions (e.g. `"CaseTable"."UserId"`) change previously-passing goldens in downstream users' trees | **certain, and intended** | Called out as an observable behavior change in §B.4 item 1 and in T5's commit message; Gate 5 requires exactly this |
| The atomic write order hides a partial failure as a `Warn` and the operator misses it | medium | The two conflated warnings are split (§A.4 step 3) and each carries `schema`, `table`, `column`, `sequence` and `expected_path` |
| N1 (undocumented, out of scope) bites a user who follows README replay advice | medium | §B.2(c) makes it explicit in the "creation order, not a replay order" paragraph, with the observed error text, and points at `merge`; §G2 asks the Delivery Lead for a decision |
| Fixing `referencesTable`/`reOwnedBy` "while we're in there" | medium | Explicitly prohibited in T2's file list and T3's; F3 explains why the expansion multiplies N1 |
| Perf claim disputed | low | §1.6 gives two paired runs with medians, mins and win/loss counts; the guard's read is hoisted per schema by construction and T2 prohibits moving it inward |

---

## F. Rejected alternatives

- **F1 — Tighten `reCol` in place, keep `inlineBigserial`/`removeDefaultNextval` (minimal-change).**
  Rejected. It fixes C1's false positive but leaves `removeDefaultNextval`'s false negative, and the
  two together newly produce a `bigserial` column whose `nextval` DEFAULT survives while its SEQUENCE
  file is deleted — a dangling reference **created by the partial fix**. Measured with
  `/tmp/bin_parsefix`: `ERROR: relation "public.custom_seq_99" does not exist` (`m_pf.sql:158`).
  It also keeps INV-1 violated, so the next interpolated name re-opens the same hole.
- **F2 — Introduce a real SQL parser (pg_query-go / hand-written grammar) for `CREATE TABLE`.**
  Rejected. New CGo dependency for a 24-line defect; effectively "re-designing `MergeTableObjects`",
  which `docs/as-goals/split-merge-by-default.md` places out of scope; puts all 23 existing tests and
  Gate 3 at risk. The chosen line scanner is enough because pg_dump's column-list shape is fixed.
- **F3 — Fix the whole identifier class now (`reOwnedBy`, `splitDotRef`, `reNameSplit`, `reOnTable`,
  `referencesTable`, `reSeqSet*`).** Rejected **for this iteration**, not forever. These sites
  currently *under*-match, which is why quoted relations survive rather than corrupt. Making them
  match expands `bigserial` conversion to quoted relations **and** relocates more `DEFAULT` bodies
  into earlier `TABLE` files, multiplying N1 (§1.5) — a defect this plan cannot fix inside scope. It
  also changes which categories merge, so Gate 4's evidence and Gate 1's smoke assertions would need
  a new gate of their own. Deferred with behavior documented in §B.1/§B.2(a).
- **F4 — Widen `serialTypeTokens` to `smallint`/`serial`/`serial8` so more sequences fold.** Rejected:
  a behavior expansion nobody asked for, no gate requires it, and each new spelling needs its own
  live-DB proof. Membership is frozen by T2's prohibitions and asserted by test 1 (`width integer`
  stays).
- **F5 — Re-introduce an opt-out flag (`--no-merge`) so C1's blast radius is user-selectable.**
  Rejected: fails Gate 1's condition outright ("the merge knob does not exist to be turned off").
- **F6 — Defer C1 as pre-existing.** Rejected: `e886939` moves a dormant defect onto the default path
  of `split`, `dump --split`, `check` and `diff` — proven by splitting the same dump with the `55d5169`
  binary, which leaves `user_id bigint NOT NULL`.
- **F7 — Fix N1 inside iteration 2 by deferring `nextval` blocks in `internal/merge` like FK blocks.**
  Rejected: `internal/merge.FromIndex` is named out of scope by the goal, and it would change `check`
  and `merge` semantics for every consumer. Escalated instead (§G2).

---

## G. Escalations for the Delivery Lead

- **G1 — W2, the question you asked.** Loud, but not an escalation: **the C1 fix does not make the
  quadratic merge cost materially worse.** Paired medians across two runs: fix-only −0.33 s / −0.60 s;
  fix+guard +0.49 s / −0.58 s / +0.08 s against a 10.06-10.45 s HEAD baseline (≈0-5 %, inside the
  run-to-run spread of ~3-4 s caused by filesystem caching). The asymptotic class is unchanged —
  `scanDefaultDir` is once per schema and its result is indexed by relation. W2, S1 and S2 stay
  deferred and are not planned here.
- **G2 — N1, a new Critical-adjacent finding (needs your decision, not the Engineer's).** Always-merge
  relocates a non-inlined sequence's `nextval` DEFAULT into a `TABLE` file that precedes its
  `SEQUENCE` file in `index.txt`, so sequential `psql -f` replay fails with
  `ERROR: relation "<schema>_<table>_seq" does not exist` **and the column silently loses its default**
  (measured: `cycled.id` `nextval('cycled_id_seq'::regclass)` → no default). `merge`/`check` inherit
  it because only FK blocks are deferred. Pre-existing at HEAD, independent of C1, and the C1 fix
  removes exactly one instance of it. Every in-scope remedy touches `index.txt` ordering or
  `internal/merge.FromIndex`, both out of scope. Iteration 2 therefore only **documents** it (T3,
  §B.2(c), §B.4 item 3). Decide: (i) accept the documented caveat, (ii) open an iteration-3 goal for
  dependency-aware index ordering / `FromIndex` deferral, or (iii) re-scope this goal.
- **G3 — Gate 4's Condition text vs. reality.** Gate 5's Condition also paraphrases "…are no longer
  emitted". The gates are immutable, so T5 satisfies them by putting the claim **and its measured
  exceptions** in the commit body. If the Delivery Lead prefers the gate text itself amended, that is
  your call to make, not the Architect's or the Engineer's.
- **G4 — Task 1 ships red.** T1 lands as a failing commit if it is committed separately. Plan assumes
  T1+T2 are one commit. If the pipeline requires per-task commits, use `--no-verify`-free staging by
  committing T1 and T2 together or accept a known-red intermediate commit; do **not** weaken T1.

---

## H. Do-not-touch list (for every downstream agent)

`docs/as-goals/split-merge-by-default/gates/*.md` · `e886939` (no amend/rebase/re-sign) ·
`version.txt` · `CHANGELOG.md` · `internal/merge/**` · `internal/pgdump/**` · `cmd/check.go` ·
`cmd/diff.go` · `cmd/merge.go` · `cmd/orphaned.go` · `cmd/dirdiff.go` · `cmd/validate.go` ·
`AGENTS.md` · `Taskfile.yml` · `.github/**` · `internal/split/split.go`'s state machine, filename
rules and path-containment logic (T3 edits its comments only) · every pre-existing test in
`internal/split/split_test.go` · `serialTypeTokens` membership · `reSeqNonStandard`.

**Critical files:** `internal/split/merge_table.go` (the defect and the fix),
`internal/split/split_test.go` (the six tests), `README.md` (Gate 4), `cmd/split.go` +
`cmd/dump.go` + `internal/split/split.go` doc comments (Gate 4/2).

**Plan size:** 5 tasks, WIP limit 1, largest task = 4 files.

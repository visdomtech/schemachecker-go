# Evidence Manifest — Iteration 1

**Goal:** `docs/as-goals/split-merge-by-default.md`
**Reviewer / evidence producer:** System Test Engineer (`multi-agent-review`, local mode). Every verdict below rests on a command **I ran myself** on the committed tree at `59ac098`. No Engineer or Architect self-report was accepted as evidence.
**Companion:** `docs/as-goals/split-merge-by-default/1/review.md` (findings C1, W1–W2, S1–S3)
**Extra verification beyond the plan:** the plan scoped Docker out. Docker + `pg_dump` 18.6 + `atlas` were available, so I additionally ran the **real** `pg_dump` round-trip checks that produced findings C1 and W1. That is why Gate 4 fails.

## Gate Status

| Gate | Status | Evidence | Owner |
|------|--------|----------|-------|
| Gate 1 — merge-always | ✅ **Pass** | `internal/split/split.go:49` `func Dump(dumpFile string, destDir string) error`; `internal/split/split.go:80-82` unconditional `MergeTableObjects(destDir)` (the `if opts.Merge` wrapper is gone). Greps: `grep -rn "Options" internal/split` → exit 1 (no output); `grep -rn "Merge bool" internal/split` → exit 1; exported symbols of `internal/split` = `Dump`, `MergeTableObjects`, `ResolveFilename` only. Unit tests on the **default** path: `TestDumpMergesChildObjectsByDefault` (`internal/split/split_test.go:1138`) and renamed `TestDumpMergesIndexAndTriggerIntoTableFile` (`:530`), both `--- PASS`. CLI smoke (built binary, no flags) tree + `index.txt` + `orphaned` exit 0 with control injections, `--merge` and `--no-merge` both exit 99 — all verbatim in §A, §B, §C. | Engineer (impl), Test Engineer (evidence) |
| Gate 2 — call-sites-migrated | ✅ **Pass** | `grep -rn "split.Dump" --include='*.go' .` → exactly six hits, all two-argument: `cmd/check.go:81`, `cmd/check.go:93`, `cmd/diff.go:72`, `cmd/diff.go:87`, `cmd/split.go:29`, `cmd/dump.go:77`. `grep -rn "split.Options" --include='*.go' .` → exit 1. `grep -rln "internal/split" --include='*.go' .` → only those 4 files, so no other caller exists (`cmd/validate.go`, `cmd/merge.go`, `cmd/orphaned.go`, `cmd/dirdiff.go` do not import it). `cmd/dump.go:17-20` comment no longer describes a "no merge" mode. `go build ./...` → exit 0 (§A). | Engineer |
| Gate 3 — suite-green | ✅ **Pass** | `go build ./...` exit 0, `go vet ./...` exit 0, `go test -race -count=1 ./...` → all `ok`, no FAIL (§A). `go test -race -count=1 -v ./internal/split/` → **23/23 top-level `--- PASS`** (45 including subtests), verbatim named lines for `TestMergePreservesIdentitySequence` and `TestMergeInlineIdentitySequence` (§D). IDENTITY intent of `55d5169` independently re-verified on a real PostgreSQL 17 `pg_dump`: `public/SEQUENCE/users_id_seq.sql` + `orca/SEQUENCE/policy_drafts_draft_id_seq.sql` survive and their TABLE files keep `bigint NOT NULL` with no `bigserial` (§E, §F). | Engineer (make it pass), Test Engineer (verify) |
| Gate 4 — docs-consistent | ❌ **Fail** | Three of four bullets PASS: `cmd/split.go:10-18` and `cmd/dump.go:17-20` corrected; `internal/split/split.go:1-8` (package doc) and `:43-48` (`Dump`) state the merge is unconditional; `grep -rn -e '--merge' -e 'no merge' -e 'raw pg_dump' README.md AGENTS.md cmd/ internal/` → exit 1 (§A). **Failing bullet:** "README `split` section output-shape text matches the observed tree". `README.md:153-156` asserts "There are **no** `INDEX/`, `TRIGGER/`, `DEFAULT/` or `FK_CONSTRAINT/` directories — that SQL is inside `TABLE/<table>.sql`." A real `pg_dump` split with the built binary emits `public/INDEX/mv_id_idx.sql` (index on a materialized view) and `public/TRIGGER/v.sql` (`INSTEAD OF` trigger on a view), and `public/INDEX/weird_name_idx.sql` for `ON public."Weird Name"` — §F. Same over-broad claim repeated at `cmd/split.go:10-18`, `cmd/dump.go:17-20`, `internal/split/split.go:4-7`. Secondary: `README.md:105` claims "the current directory … refused", not true when the cwd is reached through a symlink (§G). | Documentation Engineer (gate owner), Engineer (fix) |
| Gate 5 — release-impact-documented | ✅ **Pass** (mechanical, with caveat) | Commit subject + body verbatim in §H, including the downstream impact bullets and the rollback sentence: "There is no toggle in either direction by design: revert this commit to restore the unmerged output shape." Impact+rollback narrative written by me at `1/review.md` §8. `git diff --stat HEAD~2 -- version.txt CHANGELOG.md` → empty, exit 0; `version.txt` still `1.0.1`. `git show --stat e886939` → exactly the 8 declared files. `git status --porcelain` → empty (§I). Caveat recorded as W1/release-note note, not a gate failure: one release-note bullet ("`INDEX/` … no longer emitted") is over-broad. | Release Engineer (bench), Delivery Lead (clean tree) |

**Tally: 4 Pass / 1 Fail (Gate 4).** Independent of the gate tally, `as-goal` Step 4 DONE hygiene additionally requires "no unresolved Critical findings in `review.md`" — **C1 (Critical) is unresolved**, so the iteration cannot be DONE regardless of the gate tally.

## Return Shipments (Failed Gates)

### Gate: Gate 4 — docs-consistent
**Defect:** `README.md:153-156` (and the matching generalizations in `cmd/split.go:10-18`, `cmd/dump.go:17-20`, `internal/split/split.go:4-7`, and the `e886939` commit body) state that `INDEX/` and `TRIGGER/` directories are never emitted after the merge. They **are** emitted whenever an index/trigger's `ON` target is not a plain table: index on a `MATERIALIZED VIEW`, `INSTEAD OF` trigger on a `VIEW`/foreign table, and any `ON` target that requires quoting (`ON public."Weird Name"`, unparseable by `reOnTable`, `internal/split/merge_table.go:114`). Reproduced with the built binary against real `pg_dump` output — verbatim tree listings in §F.
**Root Cause:** the doc text was written against the *plan's* fixture, whose child objects are all owned by real tables, and against `mergeableTypes`/`contentMergeTypes` (`internal/split/merge_table.go:16,21`) read as "these directories disappear" rather than "the files they can be matched to a `TABLE/<name>.sql` disappear". `mergeByContent` only iterates names present in `<schema>/TABLE/` (`internal/split/merge_table.go:103-108`, `:119-146`), so non-table-owned children legitimately keep their files, and `updateIndexAfterMerge` (`:425-455`) keeps their index entries — the tree is consistent, the *prose* is wrong.
**Routed To:** Engineer (doc edits in `README.md`, `cmd/split.go`, `cmd/dump.go`, `internal/split/split.go`; Documentation Engineer has the gate but cannot commit).
**Priority:** Warning — the code behavior is correct and consistent; the documentation and the derived release note are not.

## Code Quality Findings
- Critical: 1 (C1 — `inlineBigserial` unanchored regex fabricates `bigserial` on `*_id` columns; dormant in released 1.0.1, newly on the default path)
- Warning: 2 (W1 — docs/release-note claim contradicted by real `pg_dump`; W2 — merge post-pass is O(tables × INDEX/TRIGGER files), 16× slower at 300 tables × 900 indexes and now paid by `split`/`dump --split`/`check`/`diff`)
- Suggestion: 3 (S1 — `destDir` precondition documented but unenforced, no reachable violation found; S2 — symlinked-cwd bypasses the `cleanOutputDir` "current directory" refusal; S3 — merge `slog.Info` logs the relative `destDir`)

**Readiness score (advisory):** 58/100 → "Significant rework likely". **Verdict: Request Changes.**

## Commits Reviewed
- `e886939`: `feat(split): always merge table-owned objects into TABLE files` — the behavior + docs change (8 files: `internal/split/split.go`, `internal/split/split_test.go`, `cmd/{split,dump,check,diff}.go`, `README.md`, `AGENTS.md`); primary artifact under review
- `59ac098`: `docs: record as-goal pipeline artifacts for split-merge-by-default` — pipeline bookkeeping only (`docs/as-goals/**`, 14 files, +1144). Reviewed for cleanliness: no source, test, README, AGENTS.md or CI file touched; contains no stale `--merge`/`split.Options` reference; goal + 5 gates + plan + PROGRESS + 7 role definitions present and self-consistent. Not judged as behavior.
- Baseline for the before/after experiments (not review scope): `b97ecd7` (`HEAD~2`, = released 1.0.1 state, merge off) and `55d5169` (IDENTITY-preservation intent whose revert-risk I tested for).

---

## Appendix — Verification log (verbatim command output)

Working directory `/Users/jiangzhaohua/codes/visdomtech/schemachecker-go`, HEAD `59ac098`, tree clean before I wrote these two artifacts.

### A. Build, vet, suite, greps, gofmt

```
$ go build ./...            ; echo "build exit=$?"   → build exit=0
$ go vet ./...              ; echo "vet exit=$?"     → vet exit=0
$ go test -race -count=1 ./...
?   	github.com/visdomtech/schemachecker-go	[no test files]
?   	github.com/visdomtech/schemachecker-go/cmd	[no test files]
?   	github.com/visdomtech/schemachecker-go/internal/checkererror	[no test files]
?   	github.com/visdomtech/schemachecker-go/internal/dirdiff	[no test files]
ok  	github.com/visdomtech/schemachecker-go/internal/merge	1.350s
?   	github.com/visdomtech/schemachecker-go/internal/orphaned	[no test files]
ok  	github.com/visdomtech/schemachecker-go/internal/pgdump	2.368s
ok  	github.com/visdomtech/schemachecker-go/internal/split	1.346s
test exit=0

$ grep -rn "Options" internal/split ; echo "exit=$?"                      → exit=1        (no output)
$ grep -rn "Merge bool" internal/split ; echo "exit=$?"                   → exit=1        (no output)
$ grep -rn "split.Options" --include='*.go' . ; echo "exit=$?"            → exit=1        (no output)
$ grep -rn "split.Dump" --include='*.go' .
./cmd/check.go:81:	if err := split.Dump(schemaDump, schemaSplit); err != nil {
./cmd/check.go:93:	if err := split.Dump(incrementalDump, incrementalSplit); err != nil {
./cmd/diff.go:72:	if err := split.Dump(incrementalDump, incrementalSplit); err != nil {
./cmd/diff.go:87:	if err := split.Dump(baselineDump, baselineSplit); err != nil {
./cmd/split.go:29:	return split.Dump(args[1], outputDir)
./cmd/dump.go:77:	return split.Dump(tmpPath, outputDir)

$ grep -rn -e '--merge' -e 'no merge' -e 'raw pg_dump' README.md AGENTS.md cmd/ internal/ ; echo "exit=$?"
→ (no output) exit=1

$ grep -rn "unmerged" --include='*.go' --include='*.md' . | grep -v '^./docs/as-goals' ; echo "exit=$?"  → exit=1
$ grep -rn -e "still available" -e "raw output" -e "opt-in" README.md AGENTS.md cmd/ internal/ ; echo "exit=$?" → exit=1

$ gofmt -l .
cmd/diff.go
internal/merge/merge.go
internal/split/merge_table.go
internal/split/split_test.go

# baseline check: HEAD~2 extracted with `git archive HEAD~2 | tar -x -C $(mktemp -d)`
$ (cd $TMP && gofmt -l .)
cmd/diff.go
internal/merge/merge.go
internal/split/merge_table.go
internal/split/split_test.go
$ (cd $TMP && gofmt -d internal/split/split_test.go | grep '^@@')
@@ -404,7 +404,7 @@
@@ -917,8 +917,8 @@
$ gofmt -d internal/split/split_test.go | grep -c '^@@'      → 2   (identical two hunks at HEAD)
```

Same four dirty files at both revisions, and `split_test.go` still has **only** the two pre-existing gofmt hunks (`@@ -404,7`, `@@ -917,8`) at HEAD — the appended `TestDumpMergesChildObjectsByDefault` + `readFile` introduced **no new hunk**. `git show e886939 -- internal/split/split_test.go` hunks: `@@ -193,7`, `@@ -266,7`, `@@ -299,7`, `@@ -330,7`, `@@ -358,7`, `@@ -527,7` (rename), `@@ -555,8`, `@@ -1134,3 +1134,143` (new test) — signature-only edits plus the append, nothing else.

### B. Gate 1 CLI smoke run (built binary, no flags, my own fixture)

```
$ go build -o /tmp/sc-smoke-te/schemachecker . ; echo "build exit=$?"   → build exit=0
$ ./schemachecker split dumpA.sql outA            # no flags
2026-10-10 16:41:32 INFO merging table objects destDir=outA
2026-10-10 16:41:32 INFO merge complete schemas_processed=2
split exit=0

$ find outA | sed "s|outA|.|" | sort
.
./EXTENSION
./EXTENSION/plpgsql.sql
./index.txt
./orca
./orca/SEQUENCE
./orca/SEQUENCE/policy_drafts_draft_id_seq.sql
./orca/TABLE
./orca/TABLE/policy_drafts.sql
./public
./public/DATA
./public/DATA/users.sql
./public/FUNCTION
./public/FUNCTION/audit.sql
./public/POLICY
./public/POLICY/users.sql
./public/SEQUENCE_SET
./public/SEQUENCE_SET/users_id_seq.sql
./public/TABLE
./public/TABLE/orders.sql
./public/TABLE/users.sql
./public/VIEW
./public/VIEW/user_emails.sql
./SCHEMAS
./SCHEMAS/orca.sql
./SETTINGS.sql

$ ./schemachecker orphaned outA/index.txt ; echo "exit=$?"   → exit=0

# child-directory absence, asserted by probe (gate fixture objects are all table-owned):
$ for d in INDEX TRIGGER DEFAULT FK_CONSTRAINT CONSTRAINT POLICY SEQUENCE FUNCTION VIEW DATA EXTENSION SCHEMAS SEQUENCE_SET; do ...
PRESENT public/POLICY   PRESENT orca/SEQUENCE   PRESENT public/FUNCTION   PRESENT public/VIEW
PRESENT public/DATA     PRESENT EXTENSION       PRESENT SCHEMAS           PRESENT public/SEQUENCE_SET
   → INDEX / TRIGGER / DEFAULT / FK_CONSTRAINT / CONSTRAINT: absent
```

Merged contents (`sed -e 's/^/| /'`):

```
| public/TABLE/users.sql
| CREATE TABLE public.users (
|     id bigserial NOT NULL,
|     email text,
|     owner_id text
| );
| ALTER TABLE ONLY public.users ADD CONSTRAINT users_pkey PRIMARY KEY (id);
|
| CREATE UNIQUE INDEX users_email_idx ON public.users USING btree (email);
|
| CREATE TRIGGER users_audit AFTER UPDATE ON public.users FOR EACH ROW EXECUTE FUNCTION public.audit();
|   (no "nextval" anywhere; public/SEQUENCE/ absent → OWNED BY sequence folded away)

| public/TABLE/orders.sql
| ALTER TABLE ONLY public.orders ADD CONSTRAINT orders_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

| orca/TABLE/policy_drafts.sql
|     draft_id bigint NOT NULL,        ← no "bigserial"; IDENTITY preserved
| orca/SEQUENCE/policy_drafts_draft_id_seq.sql = ALTER TABLE orca.policy_drafts ALTER COLUMN draft_id
|     ADD GENERATED ALWAYS AS IDENTITY (...)  ← IDENTITY SEQUENCE file survives
| public/POLICY/users.sql = CREATE POLICY users_select ON public.users ...  ← POLICY survives, named after the TABLE
```

`index.txt` (12 entries, split-emission order then pruned; every entry exists on disk, and `orphaned` proves nothing on disk is missing from it):

```
SETTINGS.sql
EXTENSION/plpgsql.sql
SCHEMAS/orca.sql
public/TABLE/users.sql
public/TABLE/orders.sql
public/POLICY/users.sql
public/FUNCTION/audit.sql
public/VIEW/user_emails.sql
orca/TABLE/policy_drafts.sql
orca/SEQUENCE/policy_drafts_draft_id_seq.sql
public/DATA/users.sql
public/SEQUENCE_SET/users_id_seq.sql
```

**Control injections proving `orphaned` checks both directions** (so `exit=0` above is evidence, not a silent pass):

```
$ printf 'public/TABLE/ghost.sql\n' >> ctrl/index.txt      # index entry, no file
$ ./schemachecker orphaned ctrl/index.txt
Files missing in filesystem but referenced from index file: 
	public/TABLE/ghost.sql
Err: orphaned files detected: orphaned files detected
control-A exit=1

$ echo "..." > ctrl2/public/TABLE/extra_noidx.sql          # file, no index entry
$ ./schemachecker orphaned ctrl2/index.txt
Files that exists in filesystem but are not referenced from index file (aka orphans): 
	public/TABLE/extra_noidx.sql
Err: orphaned files detected: orphaned files detected
control-B exit=1
```

**No flag in either direction, and exit codes:**

```
$ ./schemachecker split dumpA.sql outA2 --merge      ; echo "exit=$?"
Err: Invalid command line arguments.
Usage split [schemaExportFile] [outputDir]
Usage [command] [opts]
Where command:
    - check
    - validate
    - split
    - merge
    - orphaned
    - dump
    - diff
    - dirdiff
Options:
    --version, -v    Print version and exit
exit=99
$ ./schemachecker split dumpA.sql outA3 --no-merge   ; echo "exit=$?"   → identical usage text, exit=99
$ ./schemachecker split dumpA.sql ; echo "exit=$?"                       → exit=99
$ ./schemachecker split /nope/missing.sql outA4 ; echo "exit=$?"
Err: open dump file: open /nope/missing.sql: no such file or directory
exit=3                     # main.go:57-65 maps non-checkererror → ExitInfra=3, i.e. the merge-failure class too
$ ./schemachecker dump ./migrations outX --merge ; echo "exit=$?"        → exit=99
$ ./schemachecker dump ./migrations outX --no-merge ; echo "exit=$?"     → exit=99   (only --split accepted)
$ ./schemachecker split dumpA.sql out ; ./schemachecker split dumpA.sql out ; echo "exit=$?"  → exit=0 (CLI cleans destDir)
```

**stderr/stdout separation for the now-unconditional merge logs:**

```
$ ./schemachecker split dumpA.sql stdoutcheck 2>/dev/null   → (stdout silent)
$ ./schemachecker split dumpA.sql stderrcheck 1>/dev/null
2026-10-10 16:52:04 INFO merging table objects destDir=stderrcheck
2026-10-10 16:52:04 INFO merge complete schemas_processed=2
```

### C. Note on the plan's "expected tree" (documented trap 2)

The plan's expected tree lists `./orca/POLICY/policy_drafts.sql`, but the plan's own fixture creates **no POLICY object**, so that entry cannot be produced — the Architect's listing is wrong, the code is not. I therefore wrote a fixture that *does* include `-- Name: users users_select; Type: POLICY; Schema: public;` and confirmed `<schema>/POLICY/<table>.sql` legitimately survives merging (named after the table via `internal/split/split.go:217` + `:264-265`). Gates were judged against the gates, not against the plan's listing.

### D. Gate 3 named evidence (IDENTITY + new default-path tests, `go test -race -count=1 -v ./internal/split/`)

```
--- PASS: TestMergeInlineIdentitySequence (0.00s)
--- PASS: TestMergePreservesIdentitySequence (0.00s)
--- PASS: TestDumpMergesIndexAndTriggerIntoTableFile (0.01s)
--- PASS: TestDumpMergesChildObjectsByDefault (0.02s)
PASS
ok  	github.com/visdomtech/schemachecker-go/internal/split	1.347s
exit=0
```

Top-level count: `grep -c '^--- PASS'` → **23**, `grep -c '^func Test' internal/split/split_test.go` → **23** (all pass; 45 `PASS:` lines including the 22 subtests of `TestResolveFilename`/`TestRegexObjDesc`). Other default-path `Dump` tests still green without assertion edits: `TestDumpBasic`, `TestDumpCopyData`, `TestDumpEmptyCopy`, `TestDumpSeqSet`, `TestDumpFilenameTruncation`, plus `TestMergeTableObjects` and the nine other `TestMerge*` sequence tests.

### E. Symmetry / determinism of the merged transform (correctness of `check` and `diff`)

```
$ ./schemachecker split real.dump realout     ; ./schemachecker split real.dump realout_b
$ ./schemachecker dirdiff realout realout_b

Added (NOT in realout, in realout_b):

Removed (in realout, NOT in realout_b):

Changed:
dirdiff exit=0 (0=identical)

$ ./schemachecker split real.dump realout      # re-run into the ALREADY-USED dir (CLI cleans it)
$ ./schemachecker dirdiff realout realout_b → identical, exit=0
```

Both `check` sides (`cmd/check.go:81` and `:93`) and both `diff` sides (`cmd/diff.go:72` and `:87`) go through the identical two-argument `split.Dump`, and the merge iterates `os.ReadDir` (name-sorted) at every step (`merge_table.go:34`, `:81`, `:120`, `:208`), so the transform is deterministic and side-symmetric; `referencesTable` (`merge_table.go:150-157`) compares whole identifiers, so `users` cannot swallow `users_old` and cross-schema leakage is impossible because every path is scoped to `<schema>/TABLE/`. Order-dependent asymmetry would require two identically-named table files in one `TABLE/` directory. Residual (recorded as W1, not a symmetry break): a table on one side vs a same-named materialized view on the other changes *which directory* the child SQL lands in — a genuine schema difference, correctly surfaced as a diff.

### F. Real-`pg_dump` verification (beyond the plan's no-Docker scope; produced C1 and W1)

PostgreSQL 17 container + `pg_dump -U postgres --schema-only --no-privileges --no-owner` over a schema with IDENTITY columns, bigserial, FK, PK constraints, view trigger, matview index, RLS policy, partitioned table (`TABLE ATTACH`), quoted `"Weird Name"` table.

```
$ ./schemachecker split real.dump realout ; ./schemachecker orphaned realout/index.txt → exit=0
tree: EXTENSION/ SCHEMAS/ SETTINGS.sql index.txt
      orca/{SEQUENCE/policy_drafts_draft_id_seq.sql, TABLE/policy_drafts.sql}
      public/{FUNCTION/audit.sql, INDEX/mv_orders_idx.sql, INDEX/weird_name_idx.sql,
              MATERIALIZED VIEW/mv_orders.sql, POLICY/users.sql, ROW SECURITY/users.sql,
              SEQUENCE/part_parent_id_seq.sql, SEQUENCE/users_id_seq.sql,
              TABLE/{Weird Name.sql, audit_log.sql, orders.sql, part_child.sql, part_parent.sql, users.sql},
              TABLE ATTACH/part_child.sql, VIEW/user_emails.sql}

$ ./schemachecker-old split real.dump realout_old          # HEAD~2 binary = released 1.0.1 shape
$ diff <(cd realout_old && find . -type f|sort) <(cd realout && find . -type f|sort)
4,6d3
< ./public/DEFAULT/audit_log.sql
< ./public/DEFAULT/orders.sql
< ./public/FK_CONSTRAINT/orders.sql
9,11d5
< ./public/INDEX/orders_one_per_user.sql
< ./public/INDEX/part_child_k_idx.sql
< ./public/INDEX/users_email_idx.sql
16,17d9
< ./public/SEQUENCE/audit_log_id_seq.sql
< ./public/SEQUENCE/orders_id_seq.sql
27d18
< ./public/TRIGGER/users.sql
```

→ merge genuinely re-enabled; **`CONSTRAINT/` appears in neither tree** (confirms README.md:154). `INDEX/` survives in **both** trees for the matview and quoted-identifier indexes → **W1**. `realout/public/TABLE/orders.sql` (new) vs `realout_old/public/TABLE/orders.sql` (old):

```
new:  id bigserial NOT NULL,   user_id bigserial NOT NULL,   total numeric(10,2)
old:  id bigint  NOT NULL,     user_id bigint    NOT NULL,   total numeric(10,2)
source pg_dump: user_id bigint NOT NULL ; only OWNED BY refs: audit_log.id, orders.id
```
→ **C1**, and proof it is newly reachable on the default path.

Second real dump (view trigger + matview index only), verbatim:

```
$ ./schemachecker split trig.dump trigout → split exit=0 ; orphaned exit=0
./public/FUNCTION/tf.sql
./public/INDEX/mv_id_idx.sql
./public/MATERIALIZED VIEW/mv.sql
./public/TABLE/base.sql
./public/TRIGGER/v.sql
./public/VIEW/v.sql
./SETTINGS.sql
| CREATE TRIGGER vt INSTEAD OF INSERT ON public.v FOR EACH ROW EXECUTE FUNCTION public.tf();
| CREATE INDEX mv_id_idx ON public.mv USING btree (id);
```

C1 materiality (merged tree → migration → fresh database):

```
$ ./schemachecker merge realout/index.txt V1.0.0__rt.sql ; grep -n bigserial V1.0.0__rt.sql
51:    id bigserial NOT NULL,
57:    id bigserial NOT NULL,
58:    user_id bigserial NOT NULL,
$ docker exec ... psql -d replay -c '\d public.orders'      # after loading that migration
 user_id | bigint | not null | nextval('orders_user_id_seq'::regclass)     # sequence absent from the source schema
```

Minimal deterministic repro:

```
$ ./schemachecker split dumpC.sql outC          # foo(id, external_id, user_id, width) + seq OWNED BY foo.id
| CREATE TABLE public.foo (
|     id bigserial NOT NULL,
|     external_id bigserial NOT NULL,
|     user_id bigserial NOT NULL,
|     width integer NOT NULL
| );
```

IDENTITY intent of `55d5169` intact (`git show 55d5169 -- internal/split/merge_table.go`: `parseIdentitySequence`/`reIdentitySeq` deleted, `inlineSequences` skips any sequence without `OWNED BY`) and re-verified on the real dump: `public/SEQUENCE/users_id_seq.sql` and `orca/SEQUENCE/policy_drafts_draft_id_seq.sql` survive, `public/TABLE/users.sql` keeps `id bigint NOT NULL` with no `bigserial`.

### G. Dangerous-path guard (S2) — README.md:105 vs observed

```
# symlinked cwd (macOS /tmp -> /private/tmp): refusal BYPASSED, falls through to RemoveAll
$ cd /tmp/sc-smoke-te && ./schemachecker split dumpA.sql .
Err: clean output directory: RemoveAll .: invalid argument
exit=3                      # directory and its files survived (ls verified) — no data loss

# normal cwd: refuses exactly as documented
$ cd /Users/jiangzhaohua/sc-te-safe && schemachecker split dumpA.sql .
Err: refusing to remove dangerous output directory: .          exit=99
$ ... split dumpA.sql /            → Err: refusing to remove dangerous output directory: /            exit=99
$ ... split dumpA.sql "$HOME"      → Err: refusing to remove dangerous output directory: /Users/…     exit=99
```

### H. Gate 5 — commit message verbatim + release hygiene

```
$ git log -1 --format='%H%n%s' e886939
e886939…  feat(split): always merge table-owned objects into TABLE files

Body (verbatim, abridged only to the gate-relevant bullets):
  `schemachecker split` (and every internal caller: check, diff, dump --split) now
  always inlines INDEX, TRIGGER, DEFAULT and FK_CONSTRAINT SQL into the owning
  <schema>/TABLE/<table>.sql and deletes the per-type child files and directories.
  This was previously opt-in via the --merge flag removed in 55d5169; that commit
  left every call site on the unmerged path, so the released 1.0.1 binary silently
  stopped producing the canonical merged tree.
  Behavior change for downstream consumers of the split output tree:
  - public/INDEX/, public/TRIGGER/, public/DEFAULT/ and public/FK_CONSTRAINT/ are
    no longer emitted, and index.txt is rewritten to drop those entries
    (MergeTableObjects -> updateIndexAfterMerge). Consumers that glob those
    directories must read TABLE files instead. CONSTRAINT/ was never emitted.
  - POLICY/, SEQUENCE/, FUNCTION/, VIEW/, DATA/, EXTENSION/, SCHEMAS/ are unchanged;
    IDENTITY column sequences are still preserved as SEQUENCE files and are still
    never converted to bigserial.
  - Because FK constraints now live inside the child table's file, index.txt order
    is no longer a replay-safe topological order for sequential `psql -f`.
    merge.FromIndex defers FK blocks to a second pass and is unaffected.
  - The exported split.Options type is removed; split.Dump is now
    Dump(dumpFile, destDir string) error. There is no toggle in either direction by
    design: revert this commit to restore the unmerged output shape.
  Release-please derives version.txt and CHANGELOG.md; neither is edited here.

$ git show --stat e886939 → 8 files: AGENTS.md(4) README.md(91) cmd/check.go(4) cmd/diff.go(4)
  cmd/dump.go(7) cmd/split.go(13) internal/split/split.go(27) internal/split/split_test.go(156)
  = "253 insertions(+), 53 deletions(-)"

$ git diff --stat HEAD~2 -- version.txt CHANGELOG.md ; echo "exit=$?"     → (empty) exit=0
$ cat version.txt                                                          → 1.0.1
$ git status --porcelain ; echo "exit=$?"                                  → (empty) exit=0
$ grep -rn -e '--merge' -e 'split.Options' .github Taskfile.yml ; echo "exit=$?" → exit=1
```

FK-ordering claim independently true (not just plausible): in `realout/index.txt`, `public/TABLE/orders.sql` (which now carries `… ADD CONSTRAINT orders_user_id_fkey FOREIGN KEY … REFERENCES public.users(id)`) is entry **#3** while `public/TABLE/users.sql` is entry **#13** — sequential `psql -f` replay would fail. `merge.FromIndex` deferral claim verified in code: `internal/merge/merge.go:25-29` (doc), `:93` (`isFKBlock(block)`), `:133-154` (Phase 3 writes deferred FKs), `:279-280` (`ADD CONSTRAINT` && `FOREIGN KEY`).

### I. Tree state / scratch hygiene

`git status --porcelain` was empty at every point before I wrote `1/review.md` and `1/evidence-manifest.md` (the only two files I am permitted to write). No source, test, README, AGENTS.md or git history was modified; `git stash list` empty; no `checkout`/`reset`/`switch` was run. All experiments ran in scratch dirs (`/tmp/sc-smoke-te`, a `git archive HEAD~2` extract) and a self-created PostgreSQL container, all removed afterwards (`docker rm -f sc-te-pg`, `rm -rf` of both scratch dirs and of `~/sc-te-safe`).

### J. README factual claims audited against reality (Gate 4 item 4)

| Claim | Method | Verdict |
|---|---|---|
| `split` tree listing (`README.md:122-149`) | My fixture A run, file-set compare | **TRUE** — matches line-for-line (`index.txt`, `SETTINGS.sql`, `EXTENSION/plpgsql.sql`, `SCHEMAS/orca.sql`, `public/TABLE/{users,orders}.sql`, `public/POLICY/users.sql`, `public/FUNCTION/audit.sql`, `public/VIEW/user_emails.sql`, `public/DATA/users.sql`, `public/SEQUENCE_SET/users_id_seq.sql`, `orca/TABLE/policy_drafts.sql`, `orca/SEQUENCE/policy_drafts_draft_id_seq.sql`) |
| "no `INDEX/`, `TRIGGER/`, `DEFAULT/`, `FK_CONSTRAINT/` directories" (`:153-154`) | gate-scoped fixture | TRUE for the gate fixture, **FALSE as a general statement** (W1, §F) |
| "`CONSTRAINT/` is never emitted … appended straight into the TABLE file while splitting" (`:154-156`) | `internal/split/split.go:258-259`; old+new real trees | **TRUE** |
| "`SEQUENCE/` survives for `IDENTITY` columns and for sequences with non-standard options; plain `OWNED BY` sequence … folded into `bigserial` and its sequence file removed" (`:157-160`) | `merge_table.go:227-241`, `:318-320`, `:326-347`; real dump + fixture A | **TRUE** (but see C1: the fold is over-applied to sibling `*_id` columns, which the README does not and should not describe as intended) |
| "`POLICY/`, `FUNCTION/`, `VIEW/`, `DATA/`, `EXTENSION/`, `SCHEMAS/` keep their own files; `POLICY/` files are named after the table they protect" (`:161-163`) | fixture A + real dump (`public/POLICY/users.sql`) | **TRUE** — though the list is non-exhaustive: `MATERIALIZED VIEW/`, `ROW SECURITY/`, `TABLE ATTACH/`, `TYPE/`, `COMMENT`-derived dirs also appear |
| "`DATA/` and `SEQUENCE_SET/` only appear when the dump carries rows" (`:164-165`) | schema-only vs data runs | **TRUE** |
| "`outputDir` … is cleaned before splitting, so dangerous paths (`/`, the current directory, the home directory) are refused" (`:105`) | §G | TRUE in a normal cwd; **not literally true** under a symlinked cwd (S2) |
| "Exit codes: `0`, `3` if the dump could not be read or the merged tree could not be written, `99` on invalid arguments" (`:167-168`) | §B | **TRUE** (exit 3 empirically for the read failure; the same `main.go:57-65` mapping covers the merge-failure class) |
| "`index.txt` … FK may be emitted before the referenced table's file; `merge.FromIndex` defers FK blocks" (`:107-115`) | §F / §H | **TRUE** |
| `dump` section: `--split` produces the same merged shape; parent dir must exist without `--split`; exit codes 0/3/99 (`:225-260`) | `cmd/dump.go:29-50`, `:57`, `:77`; flag-rejection runs | **TRUE** (inherits the same W1 caveat about `INDEX/`/`TRIGGER/`) |
| `AGENTS.md:37,48` (architecture annotations) | read + behavior compare | **TRUE**; AGENTS.md contains no `--merge`/"no merge"/"raw pg_dump" claim, before or after |

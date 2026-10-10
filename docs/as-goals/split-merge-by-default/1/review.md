# Review — split-merge-by-default, Iteration 1

**Reviewer:** System Test Engineer (`multi-agent-review`, local mode, read-only through the verdict)
**Scope:** `e886939..59ac098` (`BASE=55d5169`'s child chain; behavior commit = `e886939`, bookkeeping commit = `59ac098`)
**Gates judged:** all five, re-evaluated from scratch. No prior verdict was inherited.

**Readiness Score: 58/100** (advisory — 1 Critical −20, 2 Warning −16, 3 Suggestion −6)
**Pre-Flight:** `go build ./...` ✅ exit 0 · `go vet ./...` ✅ exit 0 · `go test -race -count=1 ./...` ✅ all `ok` · `gofmt -l .` ✅ exactly the 4 pre-existing dirty files
**Dimensions reviewed:** Architecture & Impact, Correctness, Security, Performance, Completeness, Observability (fan-out performed inline, one adversarial pass per dimension).

**Gate verdicts:** Gate 1 ✅ Pass · Gate 2 ✅ Pass · Gate 3 ✅ Pass · Gate 4 ❌ **Fail** · Gate 5 ✅ Pass (mechanical, with a Warning).
**Blocking for DONE:** Critical C1 (unresolved) + Gate 4 Fail.

---

## 1. Correctness (Logician lens)

### C1 — CRITICAL: always-merge fabricates `bigserial` on any column whose name *ends with* an owned sequence's column name

**Location:** `internal/split/merge_table.go:337-338` (regex), `internal/split/merge_table.go:342` (`ReplaceAllString`), reached from `internal/split/merge_table.go:244-245` (`inlineSequences` → `inlineBigserial`), now on the default path via `internal/split/split.go:80`.

```go
reCol := regexp.MustCompile(
    `(?i)(` + regexp.QuoteMeta(columnName) + `\s+)(integer|bigint|int4|int8)(\s+NOT\s+NULL)`)
...
newContent := reCol.ReplaceAllString(content, "${1}bigserial${3}")
```

`columnName` is interpolated with no left boundary, so for a sequence `OWNED BY foo.id` the pattern `(id\s+)(integer|bigint)(\s+NOT\s+NULL)` also matches inside `user_id bigint NOT NULL`, `external_id bigint NOT NULL`, `workspace_id integer NOT NULL`, … and `ReplaceAllString` rewrites **every** occurrence.

**Why it matters:** the merged `TABLE/<table>.sql` — the tool's primary artifact — states a column type the database does not have. A `bigserial` in a replayed migration silently creates a new sequence plus a `nextval` default on an FK/tenant column. Verified end-to-end against a live PostgreSQL 17 (not argued):

- Source schema (`CREATE TABLE public.orders (id bigserial PRIMARY KEY, user_id bigint NOT NULL REFERENCES public.users(id), total numeric(10,2))`) → `pg_dump --schema-only --no-privileges --no-owner` → `user_id bigint NOT NULL`, and `grep OWNED BY` on the dump shows only `audit_log.id` and `orders.id` own sequences.
- `schemachecker split <that dump> out` → `out/public/TABLE/orders.sql`:
  `id bigserial NOT NULL,` **`user_id bigserial NOT NULL,`**
- `schemachecker merge out/index.txt V1.0.0__rt.sql` → loaded into a fresh database → `\d public.orders` reports
  `user_id | bigint | not null | nextval('orders_user_id_seq'::regclass)` — a sequence that never existed in the source schema.

Minimal deterministic repro (3 columns, one owned sequence):

```
| CREATE TABLE public.foo (
|     id bigserial NOT NULL,
|     external_id bigserial NOT NULL,
|     user_id bigserial NOT NULL,
|     width integer NOT NULL
| );
```

**Reachability is introduced by this commit.** The regex predates `e886939` and is untouched by it, but `55d5169` left the merge off at every call site, so the released 1.0.1 binary never ran it. Proven by building `HEAD~2` into a separate binary and splitting the same real dump: `realout_old/public/TABLE/orders.sql` contains `user_id bigint NOT NULL`. This change moves that dormant defect onto the happy path of `split`, `dump --split`, `check` and `diff`. Per `multi-agent-review` Phase 3.6, a pre-existing bug is in scope when the change makes it worse — here it makes it universal.

**Effect on `check`/`diff` verdicts:** the two sides stay comparable (both go through the identical transform), so this does **not** by itself flip a drift verdict; the damage is a wrong output artifact and, for the documented `dump --split` → commit tree → `check` workflow, a schema-definition side that materializes extra sequences when replayed.

**Fix (route: Engineer):** anchor the column reference to a column-definition position and restrict the substitution to the matched line, e.g.
`(?im)^(\s*"`? + quoted name + `"?\s+)(integer|bigint|int4|int8)(\s+NOT\s+NULL)` — plus require a full-token identifier match (compare `stripQuotes`-style parsing, or reuse `splitDotRef`) instead of a bare substring; and prefer a single replacement for the specific column over `ReplaceAllString`. Add a regression test with the `foo(id, external_id, user_id, width)` shape above (no existing test covers an `*_id` sibling column: `TestMergeInlineSequence*` fixtures all use a single `id` column).

---

## 2. Completeness (Cartographer lens)

### W1 — WARNING: docs (and the release note) claim the child directories are *never* emitted; real `pg_dump` output proves otherwise

**Locations:** `README.md:153-156`; commit message body of `e886939` ("`public/INDEX/`, `public/TRIGGER/`, `public/DEFAULT/` and `public/FK_CONSTRAINT/` are no longer emitted"); softer but same-shaped generalizations at `cmd/split.go:10-18`, `cmd/dump.go:17-20`, `internal/split/split.go:4-7`.

`mergeByContent` only matches `INDEX`/`TRIGGER` files against names present in `<schema>/TABLE/` (`internal/split/merge_table.go:103-108`, `:119-146`), and `referencesTable` (`:150-157`) uses `reOnTable` = ``(?i)ON\s+([\w.]+)\s*[\s(;,]`` (`:114`), which cannot parse a schema-qualified **quoted** identifier. So three ordinary real-world classes keep their own directories after the merge. Verified with a real `pg_dump` from PostgreSQL 17:

```
./public/INDEX/mv_id_idx.sql              -- CREATE INDEX mv_id_idx ON public.mv  (materialized view)
./public/TRIGGER/v.sql                    -- CREATE TRIGGER vt INSTEAD OF INSERT ON public.v  (view)
./public/INDEX/weird_name_idx.sql         -- CREATE INDEX weird_name_idx ON public."Weird Name"  (quoted/mixed-case)
```

`README.md:153` says "There are **no** `INDEX/`, `TRIGGER/`, `DEFAULT/` or `FK_CONSTRAINT/` directories — that SQL is inside `TABLE/<table>.sql`." That sentence is not literally true, and a downstream consumer reading the release note will glob `INDEX/`/`TRIGGER/` and lose matview indexes and view triggers. Note the qualified wording in `cmd/split.go` ("**table-owned** … is inlined") is defensible; the unqualified README bullet and the commit-body bullet are not.

**Fix (route: Engineer / Documentation Engineer):** scope every such claim to "table-owned" objects and state the exceptions explicitly (indexes on materialized views, triggers on views/foreign tables, and anything whose `ON` target needs quoting). Same wording fix in the release note.

### S2 — SUGGESTION: `README.md:105` advertises a dangerous-path refusal that is bypassable under a symlinked cwd

`cmd/root.go:45-51` resolves the *output* path with `filepath.EvalSymlinks` but compares it against `os.Getwd()`/`os.UserHomeDir()` unresolved. When the process cwd is reached through a symlink (macOS `/tmp` → `/private/tmp`), the "current directory" entry in the blocklist does not match, and the guard falls through to `os.RemoveAll`:

```
$ schemachecker split dumpA.sql .          # cwd = /tmp/sc-smoke-te
Err: clean output directory: RemoveAll .: invalid argument
exit=3
```

In a non-symlinked cwd the refusal works as documented (`refusing to remove dangerous output directory: .` / `/` / `$HOME`, all `exit=99`), and no data was lost — the OS rejects `RemoveAll(".")` and the directory plus its files survived. Under-resourced error text, no blast radius, pre-existing (outside `e886939`), but the README now markets the guarantee.

**Fix (route: Engineer):** `filepath.EvalSymlinks` the `cwd`/`home` entries too (or compare on `os.path`-cleaned device+inode).

---

## 3. Performance (Performer lens)

### W2 — WARNING: unconditional merge is quadratic in (tables × INDEX/TRIGGER files) and is now paid by every command

`mergeSchemaTables` iterates every `TABLE/*.sql` (`internal/split/merge_table.go:80-111`) and, **per table**, calls `mergeByContent` for `INDEX` and `TRIGGER`, which `os.ReadDir`s the whole directory and `os.ReadFile`s **every remaining file in it** (`internal/split/merge_table.go:119-146`). That is O(T × I) file opens on the default path — previously zero, because no call site enabled the merge.

Measured on the built binary (wall time, `split`, same machine, PostgreSQL-free synthetic dumps; `schemachecker-old` = `HEAD~2` binary):

| fixture | shape | new (always-merge) | old (unmerged) |
|---|---|---|---|
| big.dump | 300 tables × 900 indexes | **4.48 s** | 0.28 s (16× slower) |
| big4.dump | 30 tables × 900 indexes | 0.68 s | — |
| big2.dump | 300 tables × 1800 indexes | 11.88 s | — |
| big3.dump | 600 tables × 1800 indexes | 17.15 s | — |
| dumpA.sql / real.dump | 3–6 tables | 0.06 s | — |

30 → 300 tables at the same 900 indexes is a ~6.6× jump, and doubling both dimensions is ~3.8×: consistent with T × I. A 1000-table/5000-index schema lands in the minutes, in `check` twice (once per side) and in `diff` twice.

**Fix (route: Engineer):** one pass per `INDEX`/`TRIGGER` directory — read each file once, resolve its owning table via `referencesTable`, and append into a `map[table][]content` (or write through a per-table buffered writer), reducing the post-pass to O(I + total bytes). Preserve the current per-table append order so the merged output stays byte-identical (see "symmetry" evidence in §5).

---

## 4. Architecture & Impact (Architect lens)

### S1 — SUGGESTION: `Dump`'s "destDir must be empty or non-existent" invariant is documented but unenforced

`internal/split/split.go:47-48` states the precondition; `MergeTableObjects` appends (`internal/split/merge_table.go:179-193`) and is explicitly non-idempotent (`:28-29`). `Dump` itself checks nothing.

I tried to break it and could not find a reachable violation: all six sites own a clean target (`cmd/split.go:25`, `cmd/dump.go:57`, `cmd/check.go:34`/`os.MkdirTemp` at `:38`, `cmd/diff.go:28`), and in `check`/`diff` the `schemasplit`/`incrementalsplit`/`baselinesplit` sub-directories are never created before the split; every test uses a fresh `t.TempDir()/output` (`internal/split/split_test.go:195,268,301,332,360,557,1205`). Confirmed at the CLI: running `split real.dump realout` twice produced a tree byte-identical to an independent single run (`dirdiff` → no Added/Removed/Changed, exit 0).

Judgement: **acceptable for this change** — Suggestion, not Warning. Enforcing it inside `internal/split` would drag `cmd.cleanOutputDir`'s dangerous-path policy into an internal package (the layering rule in `AGENTS.md`), and the plan rejected that deliberately. A cheap non-policy guard is available if the team wants belt-and-braces: fail fast when `filepath.Join(destDir, "index.txt")` already exists.

**Route:** Engineer (optional hardening) — no action required to ship.

### Architect — no structural objection to the API collapse

Deleting `split.Options` and calling `MergeTableObjects` as a post-pass is the right shape: the merge needs a finished tree to resolve ownership by content (`merge_table.go:103-108`), and keeping it out of `flushTo` avoids interleaving an O(T×I) pass into the streaming parser. `internal/split` now exports exactly `Dump`, `MergeTableObjects`, `ResolveFilename` (verified by symbol grep) — no orphaned option surface, no new exported type, no second entry point. `pgdump.Options` (`internal/pgdump/pgdump.go:24`) is a distinct unrelated type; `cmd/root.go:22` `Options:` is usage text. Error wording `merge table objects: %w` (`split.go:81`) matches the file's `pkg`-style wrapping.

---

## 5. Security (Sentinel lens)

No new attack surface. `split` writes only under a user-supplied directory that `cmd.cleanOutputDir` wipes first; the parser adds a path-containment check (`internal/split/split.go:294-307`) that I re-confirmed is still on the merged path — `MergeTableObjects` derives every path from `os.ReadDir` names under `destDir`, so the merge cannot escape the tree. No secrets, no shell, no network on this path (`pg_dump` is only invoked by `internal/pgdump`, untouched). The only guard gap is the symlinked-cwd blocklist miss (S2), which is a safety/UX issue rather than a security boundary.

---

## 6. Observability (Observer lens)

- **S3 — SUGGESTION:** the merge now logs unconditionally (`internal/split/merge_table.go:31`, `:75`). Confirmed they go to **stderr** and stdout stays clean (`split dumpA.sql out 2>/dev/null` printed nothing; `1>/dev/null` printed both `INFO` lines), so `check`/`diff` stdout progress lines (`[3/6] Splitting incremental dump`) are unaffected. Minor: `destDir` is logged as supplied (`INFO merging table objects destDir=outA`), i.e. a relative path in most runs — log lines are hard to correlate across a multi-side `check`. Consider logging `filepath.Abs(destDir)`.
- **Error path rewording is complete.** `grep -rn "unmerged" --include='*.go' --include='*.md' .` outside `docs/as-goals/` → no hits; `grep -rn -e "still available" -e "raw output" -e "opt-in" README.md AGENTS.md cmd/ internal/` → exit 1. No code or doc still promises the unmerged fallback that `55d5169`+this commit removed.
- **Failure-mode visibility:** a merge failure returns a plain error from `Dump`, which `main.go:57-65` maps to exit `3` (`ExitInfra`) — the same mapping I exercised for the sibling I/O failure (`schemachecker split /nope/missing.sql out` → `Err: open dump file: …`, `exit=3`). `check`/`diff`/`dump` additionally wrap it with `checkererror.Wrap(ExitInfra, …, "split schema dump: %s")`, keeping their existing messages verbatim.
- **Coverage gap tied to C1:** no test in `internal/split` has an `OWNED BY` sequence plus a sibling `*_id` column, which is exactly why a 10-line fixture would have caught the fabrication before it shipped to the default path. Adding that case is part of C1's fix.

---

## 7. What I tried that could have broken it

| # | Attack | Result |
|---|---|---|
| 1 | Built `HEAD~2` into a separate binary and split the **same** real `pg_dump` with old vs new | Diff of file sets: `DEFAULT/`, `FK_CONSTRAINT/`, `TRIGGER/`, 3 of 5 `INDEX/` files and 2 `SEQUENCE/` files removed → the change genuinely re-enables the merge; `CONSTRAINT/` absent in **both** (proves README.md:154 "never emitted" is true). Also isolated C1 (old output kept `user_id bigint NOT NULL`). |
| 2 | Real PostgreSQL 17 container + `pg_dump --schema-only --no-privileges --no-owner` (identity cols, bigserial, FK, view trigger, matview index, RLS policy, partitioned table, `TABLE ATTACH`, quoted `"Weird Name"`) | Merged tree produced; `orphaned index.txt` exit 0. Found W1 (`INDEX/`, `TRIGGER/` legitimately survive) and C1. |
| 3 | Replayed the merged tree through `merge.FromIndex` into a fresh database | Proved C1 is material, not cosmetic: `user_id` gained `nextval('orders_user_id_seq')`. |
| 4 | Deliberately **wrong** two-token headers (plan trap 1) — `-- Name: users_pkey_idx; Type: INDEX;` vs `<relation> <object>` forms, plus a single-token `FK CONSTRAINT` header | Confirmed the pre-existing `ResolveFilename` empty-`refName` junk-file behaviour (out of scope) and that with realistic headers no `FK_CONSTRAINT/` junk appears. Fixture design validated before use. |
| 5 | Checked the Architect's "expected tree" (plan trap 2) | The plan's `./orca/POLICY/policy_drafts.sql` entry is **not** produced by its own fixture — the fixture never creates a POLICY object. My fixture A does create `CREATE POLICY … ON public.users`, and the merged tree keeps `public/POLICY/users.sql` (named after the **table**, via `split.go:217/264-265`). Judged the code against the gates, not against that listing. |
| 6 | `index.txt` ⟷ filesystem, both directions | `orphaned` exit 0 on three trees, plus two **control** injections proving the checker works: an index entry with no file → exit 1; a file with no index entry → exit 1. So exit 0 is meaningful evidence, not a silent pass. |
| 7 | Flag regression in both directions | `split dump out --merge` → exit 99 with usage text; `split dump out --no-merge` → exit 99; `dump <m> <out> --merge`/`--no-merge` → exit 99 (only `--split` accepted, `cmd/dump.go:29-34`). No flag in either direction. |
| 8 | Caller enumeration beyond `split.Dump` | `grep -rn "split.Dump" --include='*.go' .` → exactly 6 sites, all two-argument; `grep -rln "internal/split"` → only `cmd/{check,diff,split,dump}.go`; exported-symbol scan of `internal/split` shows no leftover option type. `cmd/validate.go`, `cmd/merge.go`, `cmd/orphaned.go`, `cmd/dirdiff.go` never reach the merge. |
| 9 | Stale `Options` greps scoped correctly | `grep -rn "Options" internal/split` → exit 1 (empty). `grep -rn "split.Options" --include='*.go' .` → exit 1. `grep -rn "Merge bool" internal/split` → exit 1. Bare repo-wide `Options` still matches `pgdump.Options`, `env.Options`, `cmd/root.go:22` usage text — explicitly **not** counted as Gate 1 failures. |
| 10 | Symmetry / order-dependence of the merged transform | `split` the same real dump twice into two dirs → `schemachecker dirdiff` reports no Added/Removed/Changed (exit 0). The merge iterates `os.ReadDir` (name-sorted) everywhere, so output is deterministic. `referencesTable` compares whole identifiers (`ref == tableName || ref == schema+"."+tableName`), so `users` cannot swallow `users_old`. Cross-table leakage would need two tables named identically in one `TABLE/` dir, which is impossible. |
| 11 | `55d5169` intent revert hunt | `git show 55d5169 -- internal/split/merge_table.go`: `parseIdentitySequence` deleted, `inlineSequences` skips any sequence without `OWNED BY` (`merge_table.go:227-235`). On the real dump `public/SEQUENCE/users_id_seq.sql` and `orca/SEQUENCE/policy_drafts_draft_id_seq.sql` survive and their TABLE files keep `bigint NOT NULL` with no `bigserial`. Not reverted. |
| 12 | gofmt contamination hunt | `gofmt -l .` at HEAD = `cmd/diff.go`, `internal/merge/merge.go`, `internal/split/merge_table.go`, `internal/split/split_test.go`; `git archive HEAD~2` extracted to a scratch dir gives the **same** four. `gofmt -d internal/split/split_test.go` shows exactly 2 hunks (`@@ -404,7` and `@@ -917,8`) at **both** revisions → no new formatting hunk introduced by the appended test; the new test + `readFile` helper are gofmt-clean. |
| 13 | Release-hygiene checks | `git diff --stat HEAD~2 -- version.txt CHANGELOG.md` → empty (exit 0); `version.txt` still `1.0.1`; `git status --porcelain` empty before my artifact writes; `git show --stat e886939` = exactly the 8 declared files; `59ac098` touches only `docs/as-goals/**` (bookkeeping, no behavior). `.github/` and `Taskfile.yml` contain no `--merge`/`split.Options` reference. |
| 14 | Perf scaling hunt | 4 synthetic sizes (30/300/600 tables × 900/1800 indexes) + old-binary baseline → O(T×I) confirmed, 16× at 300×900 (W2). |

---

## 8. Release impact & rollback (Gate 5 evidence, per the gate's "note in `review.md`" option)

**Observable behavior change for downstream consumers of the released binary:** `split`, `dump --split`, `check` and `diff` now always inline **table-owned** `INDEX`/`TRIGGER`/`DEFAULT`/`FK_CONSTRAINT` SQL into `<schema>/TABLE/<table>.sql` and delete those child files; `index.txt` is rewritten to drop entries for files the merge removed. `CONSTRAINT/` was never emitted (`ResolveFilename` sends `CONSTRAINT` straight into the TABLE file, `internal/split/split.go:258-259`). Directories that can still appear after the merge: `POLICY/`, `SEQUENCE/`, `FUNCTION/`, `VIEW/`, `MATERIALIZED VIEW/`, `DATA/`, `EXTENSION/`, `SCHEMAS/`, `ROW SECURITY/`, `TABLE ATTACH/`, `SEQUENCE_SET/` — **and `INDEX/`/`TRIGGER/` for objects whose `ON` target is not a plain table** (W1). Because FK statements move into the child table's file, `index.txt` order is no longer replay-safe for sequential `psql -f`; `merge.FromIndex` is unaffected (verified in `internal/merge/merge.go:25-29`, `:93`, `:133-154`, `isFKBlock` `:279-280`). The exported `split.Options` type is gone; `split.Dump` is `Dump(dumpFile, destDir string) error`.

**Rollback path:** revert `e886939` — by design there is no toggle to add in either direction, and no data migration is involved (the tool is stateless w.r.t. the database). `version.txt`/`CHANGELOG.md` are release-please's, untouched here (`git diff --stat HEAD~2 -- version.txt CHANGELOG.md` → empty).

**Release-note caveat the Release Engineer must carry:** the `INDEX/`-never-emitted bullet is over-broad (W1), and the note does not currently mention that `*_id` columns adjacent to a serial `id` get rewritten to `bigserial` (C1). C1 is a behavior regression relative to the published 1.0.1 output, so it belongs in the release note even if it gets fixed in a follow-up commit.

---

## 9. Findings summary

| ID | Severity | Dimension | Title | Route |
|---|---|---|---|---|
| C1 | **Critical** | Correctness | `inlineBigserial` unanchored regex fabricates `bigserial` on `*_id` columns; newly on the default path | **Engineer** |
| W1 | Warning | Completeness | README.md:153-156 + commit body claim `INDEX/`/`TRIGGER/` never emitted; real `pg_dump` proves otherwise | Engineer / Documentation Engineer |
| W2 | Warning | Performance | Merge post-pass is O(tables × INDEX/TRIGGER files); 16× slower at 300 tables × 900 indexes, now paid by every command | Engineer |
| S1 | Suggestion | Architecture | `Dump`'s fresh-`destDir` precondition is documented but unenforced (no reachable violation found) | Engineer (optional) |
| S2 | Suggestion | Security/Observability | Symlinked-cwd bypasses the `cleanOutputDir` "current directory" refusal; `README.md:105` overstates the guard | Engineer |
| S3 | Suggestion | Observability | Merge `slog.Info` logs the relative `destDir`; correct stderr/stdout split confirmed | Engineer (optional) |

**Verdict: Request Changes** (1 Critical + 2 Warnings). Gate 4 fails; Gates 1, 2, 3, 5 pass on evidence I produced personally. Per `as-goal` Step 4, DONE hygiene additionally requires "no unresolved Critical findings", so this iteration cannot be DONE even with Gate 4 fixed — C1 is the iteration's focus.

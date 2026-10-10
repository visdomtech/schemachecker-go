# Plan — split-merge-by-default (iteration 1)

Owner: System Architect (`multi-agent-planning`). Read-only: this plan was produced without
mutating the tree (verified `git status --porcelain` → only pre-existing `?? docs/`, HEAD `b97ecd7`).
All findings below were confirmed empirically against a scratch copy at `/tmp/sc-scratch`.

## Critical files

- `internal/split/split.go` — the knob and the call site of `MergeTableObjects`
- `internal/split/merge_table.go` — the merge itself (not modified by this plan)
- `internal/split/split_test.go` — all 6 test call sites + the new default-path proof
- `cmd/check.go`, `cmd/diff.go`, `cmd/split.go`, `cmd/dump.go` — the 6 production call sites
- `README.md` — the `split` output-shape section that currently documents the unmerged tree

## Architecture decision

Delete `split.Options` outright and make `Dump(dumpFile, destDir string) error` run
`MergeTableObjects(destDir)` unconditionally as a post-pass, placed after the final
`buf.flushTo(...)` and before `return nil` (`internal/split/split.go:73-82`). The merge stays a
*post-pass over the finished tree*, not something interleaved into `flushTo`, because
content-matching (`mergeByContent` for `INDEX`/`TRIGGER`, `reOnTable` at
`internal/split/merge_table.go:114-157`) needs sibling files already on disk to resolve table
ownership. All six production call sites and all six test call sites are migrated in the same
commit so the package never sits in a non-compiling state at HEAD.

### Rejected alternatives

| Alternative | Why it lost |
|---|---|
| Keep `Options` as an empty struct / future extension point (`Dump(f, d string, opts Options)`) | Gate 1 requires "internal/split exports no `Options` type". A zero-field struct is pure ceremony: 7+ call sites must keep writing `Options{}` forever for no behavior. |
| Keep `Options.Merge` but default it on (e.g. `NewOptions()` / `NoMerge bool` inversion) | Any field is an opt-out knob, which the goal forbids ("no escape-hatch flag"). Go's zero value cannot express "default true" without a constructor, and a constructor just re-adds the surface Gate 1 deletes. |
| Move merging *inside* `flushTo` / the state machine (streaming merge) | Out of scope (goal: "re-designing `MergeTableObjects`" excluded) and technically wrong: `INDEX`/`TRIGGER` files are named after the object, not the table, so ownership can only be resolved after all objects for that table exist. |
| Add `DumpMerged()` and delete/rename `Dump()` (two exported entry points) | Doubles the public surface and leaves a footgun; Gate 2's evidence is "every `split.Dump` hit uses the two-argument form", which a second name defeats. |
| Call `MergeTableObjects` from `cmd/` at each of the 6 sites instead of inside `Dump` | 6 copies of an ordering contract ("merge after the whole split") that the next call site can silently forget. The user already scoped merging to `split.Dump`. |
| Add `--no-merge` for raw output | Explicitly forbidden by the goal's Constraints. |
| Make `MergeTableObjects` idempotent instead of relying on fresh dirs | Out of scope, and unnecessary: every production site already cleans its target (§Q3). Non-idempotence note at `merge_table.go:27-29` stays. |
| Delete `split.Dump`'s destDir argument handling and let `Dump` clean the dir itself | Moves `checkererror`/dangerous-path policy (`cmd/root.go:36-62`) into `internal/`; violates the layering in AGENTS.md (internal packages return plain errors, `cmd/` owns usage safety). |

## Answers to the assigned questions

### Q1 — Which existing tests assert the UNMERGED shape and break under always-merge?

**None. Zero existing assertions had to change.** This is the plan's most important correction to
the orchestrator's premise, and it is measured, not argued:

```sh
# in a scratch copy with the change applied
go test -race -count=1 ./...            # all packages ok
go test -race -count=1 -v ./internal/split/... | grep -E '^(--- |ok|FAIL)'
```

All 21 tests in `internal/split` PASS with merge unconditional and *no assertion edits*
(`TestDumpBasic`, `TestDumpCopyData`, `TestDumpEmptyCopy`, `TestDumpSeqSet`,
`TestDumpFilenameTruncation`, `TestMergeTableObjects`, `TestDumpWithMerge`, the 9
`TestMerge*` sequence/IDENTITY tests, the 4 `TestRegex*`/`TestResolveFilename` tests).

Reason: every `Dump`-based test feeds a synthetic dump whose only objects are `TABLE`,
`TABLE DATA`, `SEQUENCE SET` and one truncated `TABLE` — none of the child types
(`INDEX`/`TRIGGER`/`DEFAULT`/`CONSTRAINT`/`FK CONSTRAINT`) that merging relocates, and none of
them asserts a directory layout that merging deletes. `TestDumpBasic`'s `index.txt` assertions
(`split_test.go:227-235`) only require `SETTINGS.sql` + two `TABLE` entries, all of which
survive `updateIndexAfterMerge`.

Per-test instructions therefore reduce to **signature-only edits** plus one rename:

| File:line | Test | Change |
|---|---|---|
| `internal/split/split_test.go:196` | `TestDumpBasic` | `Dump(dumpFile, destDir, Options{})` → `Dump(dumpFile, destDir)`. Assertions untouched. |
| `internal/split/split_test.go:269` | `TestDumpCopyData` | same. `DATA/users.sql` is not a mergeable type → survives merge. |
| `internal/split/split_test.go:302` | `TestDumpEmptyCopy` | same. |
| `internal/split/split_test.go:333` | `TestDumpSeqSet` | same. |
| `internal/split/split_test.go:361` | `TestDumpFilenameTruncation` | same (note: this one is `err := Dump(...)`, not an `if`). |
| `internal/split/split_test.go:530,558` | `TestDumpWithMerge` | rename to `TestDumpMergesIndexAndTriggerIntoTableFile`, `Options{Merge: true}` → dropped. It is now the plain default path; keep its assertions verbatim. |

The real hidden work is the opposite of the hypothesized one: **the default path was never
proven to merge**, so the new test in Task 2 is mandatory, not optional.

### Q2 — index.txt consistency after merge; orphan-detection behavior

`MergeTableObjects` *does* rewrite `index.txt`: `internal/split/merge_table.go:71` calls
`updateIndexAfterMerge` (`:423-455`), which re-reads `index.txt`, keeps only entries whose file
still exists on disk, and rewrites the file in original order. Files deleted by the merge are
removed by `os.Remove` in `appendIfExists` (`:175`) and `mergeByContent` (`:140`), so the prune
is complete. The merge never *creates* a file that isn't already indexed (`inlineBigserial`
rewrites an existing TABLE file in place), so there is no "on disk but not in index" direction.

The test asserting `index.txt` must NOT contain `public/DEFAULT/`, `public/FK_CONSTRAINT/`,
`public/CONSTRAINT/` is `TestMergeTableObjects` at `internal/split/split_test.go:517-527`. It is
maintained **solely** by `updateIndexAfterMerge`, and it calls `MergeTableObjects` directly — so
it is unaffected by this change and cannot regress.

`internal/orphaned.FromIndex` (`internal/orphaned/orphaned.go:15-87`) needs no change and its
verdict does not change: it compares `index.txt` (non-comment, non-blank lines) against every
file under the index's directory and it excludes the index file itself (`:55-56`). Post-merge
trees are consistent by construction. Verified on a real CLI run:
`schemachecker orphaned <merged dir>/index.txt` → exit `0`.

Two facts worth recording because they look like defects but are pre-existing and out of scope:

- `public/CONSTRAINT/` is **never created by the splitter at all**: `ResolveFilename` maps
  `CONSTRAINT` to `<schema>/TABLE/<refName>.sql` (`internal/split/split.go:257-258`), i.e. the
  splitter already appends constraint SQL into the TABLE file itself (via `flushTo`'s `O_APPEND`,
  with `isNew == false` so no extra index entry). Consequently `"CONSTRAINT"` in
  `mergeableTypes` (`merge_table.go:16`) is dead in the split path, and Gate 1's "no `CONSTRAINT/`
  files" bullet is trivially satisfied. README:118-119 is wrong both before and after this change.
- `POLICY` is in neither `mergeableTypes` nor `contentMergeTypes`, so `<schema>/POLICY/*.sql`
  legitimately survives merging. Gate 1 does not list `POLICY/` among the absent directories —
  the smoke fixture must therefore not treat a surviving `POLICY/` dir as a failure.

### Q3 — Fresh-directory guarantee at all 6 call sites

**No architectural defect found.** Every site writes into a directory that cannot contain stale
output. Evidence:

| Call site | destDir | Fresh because |
|---|---|---|
| `cmd/split.go:22` | `args[2]` | `cleanOutputDir(args[2])` at `cmd/split.go:18` → `os.RemoveAll` + `os.MkdirAll` (`cmd/root.go:56-61`), after refusing `/`, cwd, `$HOME`, `/etc`, `/usr`, `/var`, `/tmp`, `/boot` (`:49-53`) with symlink resolution (`:46`). |
| `cmd/dump.go:76` (`dumpAndSplit`) | `outputDir` | `cleanOutputDir(outputDir)` at `cmd/dump.go:56`, with the comment "Clear the output directory to avoid appending to stale files." |
| `cmd/check.go:81` | `<outputDir>/schemasplit` | `outputDir` is either cleaned at `cmd/check.go:34` or created by `os.MkdirTemp` at `:38`; either way the `schemasplit` **subdirectory does not pre-exist** — only `schemaMigrations` is created under it (`:55`). |
| `cmd/check.go:93` | `<outputDir>/incrementalsplit` | same reasoning; sibling of a never-created dir, and `incrementalDump.sql` is a file, not a tree. |
| `cmd/diff.go:72` | `<outputDir>/incrementalsplit` | `cleanOutputDir(outputDir)` at `cmd/diff.go:28`; subdir not created beforehand (`baselineMigrations` is, at `:37`). |
| `cmd/diff.go:87` | `<outputDir>/baselinesplit` | same. |

The only `split.Dump` callers in the repo are those six (`grep -rn "split.Dump" --include='*.go' .`
→ exactly `cmd/check.go:81,93`, `cmd/diff.go:72,87`, `cmd/split.go:22`, `cmd/dump.go:76`).
`cmd/validate.go`, `cmd/merge.go`, `cmd/orphaned.go`, `cmd/dirdiff.go` never call it, so merge
cannot reach a user-maintained (non-fresh) tree through any other path.

Residual (documented, not fixed): `split.Dump` itself has **no** guard — the invariant lives
entirely in `cmd.cleanOutputDir`. A future call site, or a test reusing one `destDir` for two
`Dump` calls, would silently double-append. Mitigation is a doc-comment contract in Task 1.

### Q4 — API shape: delete `Options` outright

Delete it. Nothing outside `internal/split` references the type except the six
`split.Options{}` literals listed above; `main.go` and `internal/checkererror` do not mention it;
`cmd/root.go:22`'s `Options:` is usage text, not the symbol. `pgdump.Options`
(`internal/pgdump/pgdump.go:24-34`, consumed by `DumpFromPool(ctx, pool, opts Options)` at `:34`
and constructed at `:209`) is an entirely separate type in a separate package — do not touch it.

Implementation detail that matters for the gate greps: after deletion,
`grep -rn "Options" internal/split` must be **empty** (Gate 1). A repo-wide bare
`grep -rn Options` will still legitimately match `pgdump.Options`, `env.Options{Prefix:"DB_"}`
(`pgdump.go:197`) and `cmd/root.go:22` — the Test Engineer must scope Gate 1's grep to
`internal/split` and use `grep -rn "split.Options"` repo-wide (which returns nothing).

Error-string detail: the current wrap at `internal/split/split.go:79` reads
`"merge post-processing failed (unmerged split still available at %s)"`. Once merging is
mandatory there is no unmerged fallback to point at, so that parenthetical becomes a false promise
to the operator. Reword to `"merge table objects: %w"` (matches the terse `pkg`-style wrapping
used elsewhere in the file, e.g. `:49`, `:64`, `:74`).

### Q5 — Is unconditional merging ever actively wrong at some path?

No correctness break at any of the 6 sites; `check`/`diff` stay symmetric because both sides go
through the identical merged transform. Three things must change or be flagged, none of which is
an objection to always-merge:

1. `cmd/dump.go:19`'s doc comment advertises "`dump --split` … per-object files (no merge,
   preserving raw pg_dump representation)". No *code* depends on raw output — it is a stale claim
   only. Gate 2 lists it as an exit item; fixed in Task 3.
2. `internal/split/split.go:79`'s "unmerged split still available" fallback text (see Q4).
3. Genuine downstream semantic change (Gate 5 material, not a blocker): merging relocates
   `ALTER TABLE … ADD CONSTRAINT … FOREIGN KEY …` into `TABLE/<table>.sql`, so **index.txt order
   is no longer a replay-safe topological order**. Empirically, a child table whose FK references a
   later-sorting table now carries the FK inside its own file, which appears *before* the
   referenced table's file in `index.txt`. The in-repo consumer is safe —
   `internal/merge.FromIndex` splits each file into statement blocks and defers every
   `ADD CONSTRAINT … FOREIGN KEY` block to a second pass (`internal/merge/merge.go:20-33`,
   `:86-102`, `:134-163`, added by `ffcf7b2`), so `check`'s index→migration path still orders FKs
   after all `CREATE TABLE`s. External consumers that `psql -f` files in `index.txt` order are the
   ones affected and must be told in the release note. README:91 currently markets `index.txt` as
   "records the file creation order (useful for replay)" — Task 3 must add the caveat.

Also cosmetic and worth stating so nobody "fixes" it: `MergeTableObjects` now always emits two
`slog.Info` lines (`merge_table.go:31`, `:75`) on **stderr** (verified: `2>/dev/null` silences
them). stdout progress lines used by `check`/`diff`/`dump` are unaffected.

## Ordered tasks (sequential; WIP limit = one Engineer at a time)

### Task 1 — Collapse the API and unconditionally merge (Engineer) — serves Gates 1 + 2

Files (5): `internal/split/split.go`, `cmd/split.go`, `cmd/dump.go`, `cmd/check.go`, `cmd/diff.go`

1. `internal/split/split.go`: delete lines 38-43 (`// Options configures the split behavior.`
   through the closing `}`) including the following blank line.
2. `internal/split/split.go:45-46`: signature becomes
   `func Dump(dumpFile string, destDir string) error`. Rewrite its doc comment to state merging
   is unconditional and that `destDir` must be empty/fresh (`MergeTableObjects` is not
   idempotent). Suggested:
   ```go
   // Dump splits the pg_dump file at dumpFile into per-object SQL files under destDir,
   // then unconditionally inlines table-owned INDEX, TRIGGER, DEFAULT and FK_CONSTRAINT
   // SQL into the corresponding TABLE file (see MergeTableObjects).
   //
   // destDir must be empty or non-existent: the merge appends to TABLE files and is not
   // idempotent. Callers own cleaning it (see cmd.cleanOutputDir).
   ```
   Keep the package doc at `:1-2` accurate — Task 3 adds the merged-shape sentence there.
3. `internal/split/split.go:77-81`: replace the `if opts.Merge { ... }` block with an
   unconditional call in the same position (after the final `flushTo` at `:73-75`, before
   `return nil`):
   ```go
   if err := MergeTableObjects(destDir); err != nil {
       return fmt.Errorf("merge table objects: %w", err)
   }
   ```
4. Strip the third argument at the six production call sites: `cmd/split.go:22`,
   `cmd/dump.go:76`, `cmd/check.go:81`, `cmd/check.go:93`, `cmd/diff.go:72`, `cmd/diff.go:87`.
   Touch nothing else in those five files (doc comments are Task 3's; the `checkererror.Wrap`
   messages stay verbatim so `check`/`diff` error text is unchanged).

Acceptance / verification:

- `go build ./...` exits 0.
- `grep -rn "Options" internal/split` → no output.
- `grep -rn "Merge bool" internal/split` → no output.
- `grep -rn "split.Options" --include='*.go' .` → no output.
- `grep -rn "split.Dump" --include='*.go' .` → exactly the six sites, all two-argument.
- `grep -n "func Dump" internal/split/split.go` → `func Dump(dumpFile string, destDir string) error`.

**Expected transient state:** `go vet ./...` and `go test ./internal/split/...` are RED at the end
of this task, because `internal/split/split_test.go` is in-package and still names `Options`.
Do not commit here; Task 2 restores green before the single commit.

### Task 2 — Retire the merge variant, prove the default path (Engineer) — serves Gates 1 + 3

File (1): `internal/split/split_test.go`

1. Signature-only edits at lines 196, 269, 302, 333, 361 per the Q1 table.
2. Rename `TestDumpWithMerge` (`:530`) → `TestDumpMergesIndexAndTriggerIntoTableFile`; drop
   `Options{Merge: true}` at `:558`. Keep all its assertions (`:562-596`) exactly as they are —
   including the "empty directories removed" loop over `public/INDEX|TRIGGER|DEFAULT|FK_CONSTRAINT|CONSTRAINT`
   and the "TABLE directory still exists" check.
3. Add `TestDumpMergesChildObjectsByDefault` (full verified source in the Test plan below).
4. Add the package-internal helper `readFile(t, destDir, rel)` used by the new test.

Acceptance / verification:

- `go vet ./...` exits 0.
- `go test -race -count=1 ./...` exits 0.
- `go test -race -count=1 -v -run 'TestDumpMergesChildObjectsByDefault|TestDumpMergesIndexAndTriggerIntoTableFile|TestMergePreservesIdentitySequence|TestMergeInlineIdentitySequence' ./internal/split/`
  → four `--- PASS` lines, no `FAIL`.
- New code is gofmt-clean (`gofmt -d internal/split/split_test.go` must show no hunk inside the
  new test). **Do not reformat the whole file**: `cmd/diff.go`, `internal/merge/merge.go`,
  `internal/split/merge_table.go` and `internal/split/split_test.go` are already gofmt-dirty at
  HEAD (verified with `gofmt -l .`), and `task ci` does not run gofmt. Wholesale reformatting is
  out-of-scope churn.

### Task 3 — Documentation catch-up (Documentation Engineer) — serves Gate 4

Files (5): `cmd/split.go`, `cmd/dump.go`, `internal/split/split.go`, `README.md`, `AGENTS.md`

1. `cmd/split.go:10-11` — replace "Splits a pg_dump SQL file into per-object files without merge,
   preserving the raw pg_dump representation for round-trip consistency." with a statement that
   output is always merged: table-owned `INDEX`, `TRIGGER`, `DEFAULT` and `FK_CONSTRAINT` SQL is
   inlined into `<schema>/TABLE/<table>.sql`; `POLICY`, `SEQUENCE` (incl. IDENTITY), `FUNCTION`,
   `VIEW`, `DATA`, `EXTENSION`, `SCHEMAS` keep their own files.
2. `cmd/dump.go:17-19` — drop "(no merge, preserving raw pg_dump representation)"; state that
   `--split` produces the same always-merged per-object tree.
3. `internal/split/split.go:1-2` (package doc) — add the unconditional-merge sentence.
4. `README.md`:
   - `:91` — keep "records the file creation order", add that table-owned child objects are merged
     into the TABLE file and that FK constraints may therefore reference a table whose file appears
     later in the index (`merge.FromIndex` defers them; sequential `psql -f` replay does not).
   - `:102-127` — replace the tree sample with the tree actually observed in the Gate-1 smoke run
     (below): delete `CONSTRAINT/`, `FK_CONSTRAINT/`, `INDEX/`, `DEFAULT/`, `TRIGGER/`; keep
     `SEQUENCE/`, `FUNCTION/`, add `POLICY/`; note that FK/INDEX/TRIGGER/DEFAULT SQL now lives
     inside `TABLE/<table>.sql`.
   - `:187-210` (`dump` section) — document the currently-undocumented `--split` variant and point
     it at the merged output shape.
5. `AGENTS.md` — in the architecture tree, annotate `split/` ("pg_dump SQL splitting into
   per-object files, child objects merged into TABLE files") and `split.go RunSplit` similarly.
   `AGENTS.md` currently contains no `--merge`/"no merge" claim, so this is an affirmative
   clarification, not a correction.

Acceptance / verification:

- `grep -rn -e '--merge' -e 'no merge' -e 'raw pg_dump' README.md AGENTS.md cmd/ internal/` → no output.
- `grep -n 'CONSTRAINT/\|FK_CONSTRAINT/\|INDEX/\|DEFAULT/\|TRIGGER/' README.md` → only the
  explanatory sentence, never inside the tree code block.
- README's `split` tree listing matches the smoke-run `find` output line-for-line.
- `go build ./...` still green (comment-only edits).

### Task 4 — Commit + release impact (Release Engineer, then Delivery Lead) — serves Gate 5

No source edits. Single commit of Tasks 1-3 (message below in the commit-draft section). Verify
before handing off:

- `git diff --stat HEAD~1 -- version.txt CHANGELOG.md` → empty (release-please owns both;
  `version.txt` must stay `1.0.1`).
- `git log -1 --stat` shows exactly the 9 files from Tasks 1-3 (5 + 1 + 5, minus overlap:
  `internal/split/split.go`, `cmd/{split,dump,check,diff}.go`, `internal/split/split_test.go`,
  `README.md`, `AGENTS.md`).
- `git status` clean; `task ci` green on the committed tree.

## Test plan

**Existing tests:** per the Q1 table — signature-only edits, zero assertion rewrites, and
`TestDumpWithMerge` renamed into a default-path test. Nothing else changes. The IDENTITY
regression guard for Gate 3 is `TestMergePreservesIdentitySequence`
(`internal/split/split_test.go:883`) + `TestMergeInlineIdentitySequence` (`:821`) — both direct
`MergeTableObjects` calls, so they prove the merge logic itself still preserves IDENTITY. They are
necessary but no longer sufficient, because they bypass `Dump`; Task 2 therefore adds an IDENTITY
assertion **on the default `Dump` path** (`orca/SEQUENCE/policy_drafts_draft_id_seq.sql` preserved,
`orca/TABLE/policy_drafts.sql` keeps `bigint NOT NULL` and contains no `bigserial`) — verified
green in the scratch run. Also unaffected and useful guards: `TestMergeInlineSequence` /
`...Bigint` / `...NoCycleIsStandard` / `...QuotedIdentifiers` (the `bigserial` conversion for
`OWNED BY` sequences) and `TestMergeInlineSequenceNonStandard` /
`...NegativeMinvalue` / `...TableMissing` (must-not-inline cases).

**New test — `TestDumpMergesChildObjectsByDefault`** (Task 2, verified passing in scratch). Uses
realistic two-token pg_dump headers, covers all five child types, asserts the index is pruned and
index-vs-filesystem consistent:

```go
func TestDumpMergesChildObjectsByDefault(t *testing.T) {
	tmpDir := t.TempDir()
	dumpFile := filepath.Join(tmpDir, "dump.sql")

	// Realistic pg_dump headers: child objects use the two-token
	// "-- Name: <relation> <object>; Type: ...;" form.
	dumpContent := strings.Join([]string{
		"SET statement_timeout = 0;",
		"",
		"-- Name: users; Type: TABLE; Schema: public; Owner: postgres",
		"CREATE TABLE public.users (",
		"    id integer NOT NULL,",
		"    email text",
		");",
		"",
		"-- Name: orders; Type: TABLE; Schema: public; Owner: postgres",
		"CREATE TABLE public.orders (",
		"    id integer NOT NULL,",
		"    user_id integer",
		");",
		"",
		"-- Name: users_id_seq; Type: SEQUENCE; Schema: public; Owner: postgres",
		"CREATE SEQUENCE public.users_id_seq",
		"    START WITH 1",
		"    INCREMENT BY 1",
		"    NO MINVALUE",
		"    NO MAXVALUE",
		"    CACHE 1;",
		"",
		"-- Name: users_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: postgres",
		"ALTER SEQUENCE public.users_id_seq OWNED BY public.users.id;",
		"",
		"-- Name: users id; Type: DEFAULT; Schema: public; Owner: postgres",
		"ALTER TABLE ONLY public.users ALTER COLUMN id SET DEFAULT nextval('public.users_id_seq'::regclass);",
		"",
		"-- Name: users users_pkey; Type: CONSTRAINT; Schema: public; Owner: postgres",
		"ALTER TABLE ONLY public.users ADD CONSTRAINT users_pkey PRIMARY KEY (id);",
		"",
		"-- Name: users_email_idx; Type: INDEX; Schema: public; Owner: postgres",
		"CREATE UNIQUE INDEX users_email_idx ON public.users USING btree (email);",
		"",
		"-- Name: orders orders_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: postgres",
		"ALTER TABLE ONLY public.orders ADD CONSTRAINT orders_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;",
		"",
		"-- Name: users users_audit; Type: TRIGGER; Schema: public; Owner: postgres",
		"CREATE TRIGGER users_audit AFTER UPDATE ON public.users FOR EACH ROW EXECUTE FUNCTION public.audit();",
		"",
		"-- Name: policy_drafts; Type: TABLE; Schema: orca; Owner: postgres",
		"CREATE TABLE orca.policy_drafts (",
		"    draft_id bigint NOT NULL,",
		"    workspace_id text NOT NULL",
		");",
		"",
		"-- Name: policy_drafts_draft_id_seq; Type: SEQUENCE; Schema: orca; Owner: postgres",
		"ALTER TABLE orca.policy_drafts ALTER COLUMN draft_id ADD GENERATED ALWAYS AS IDENTITY (",
		"    SEQUENCE NAME orca.policy_drafts_draft_id_seq",
		"    START WITH 1",
		"    NO MINVALUE",
		"    CACHE 1",
		");",
		"",
	}, "\n")

	if err := os.WriteFile(dumpFile, []byte(dumpContent), 0o644); err != nil {
		t.Fatal(err)
	}

	destDir := filepath.Join(tmpDir, "output")
	if err := Dump(dumpFile, destDir); err != nil {
		t.Fatalf("Dump failed: %v", err)
	}

	users := readFile(t, destDir, "public/TABLE/users.sql")
	for _, want := range []string{
		"CREATE TABLE public.users",
		"ADD CONSTRAINT users_pkey PRIMARY KEY",
		"CREATE UNIQUE INDEX users_email_idx",
		"CREATE TRIGGER users_audit",
	} {
		if !strings.Contains(users, want) {
			t.Errorf("public/TABLE/users.sql should contain %q, got:\n%s", want, users)
		}
	}
	if !strings.Contains(users, "bigserial") {
		t.Errorf("standard OWNED BY sequence should still inline as bigserial, got:\n%s", users)
	}
	if strings.Contains(users, "nextval") {
		t.Errorf("inlined nextval DEFAULT should be gone, got:\n%s", users)
	}

	orders := readFile(t, destDir, "public/TABLE/orders.sql")
	if !strings.Contains(orders, "orders_user_id_fkey FOREIGN KEY") {
		t.Errorf("public/TABLE/orders.sql should contain the inlined FK, got:\n%s", orders)
	}

	for _, dir := range []string{"public/INDEX", "public/TRIGGER", "public/DEFAULT", "public/FK_CONSTRAINT", "public/CONSTRAINT"} {
		if _, err := os.Stat(filepath.Join(destDir, dir)); !os.IsNotExist(err) {
			t.Errorf("%s should not exist in merged output", dir)
		}
	}

	// IDENTITY preservation must survive the default merge path (gate 3).
	pd := readFile(t, destDir, "orca/TABLE/policy_drafts.sql")
	if strings.Contains(pd, "bigserial") {
		t.Errorf("IDENTITY column must not become bigserial, got:\n%s", pd)
	}
	if !strings.Contains(pd, "bigint NOT NULL") {
		t.Errorf("IDENTITY column must stay bigint NOT NULL, got:\n%s", pd)
	}
	if _, err := os.Stat(filepath.Join(destDir, "orca/SEQUENCE/policy_drafts_draft_id_seq.sql")); os.IsNotExist(err) {
		t.Error("IDENTITY SEQUENCE file must be preserved")
	}

	// index.txt must be pruned and every remaining entry must exist on disk.
	idx := readFile(t, destDir, "index.txt")
	for _, notWant := range []string{"public/INDEX/", "public/TRIGGER/", "public/DEFAULT/", "public/FK_CONSTRAINT/", "public/CONSTRAINT/"} {
		if strings.Contains(idx, notWant) {
			t.Errorf("index.txt should not contain %q, got:\n%s", notWant, idx)
		}
	}
	for _, entry := range strings.Split(strings.TrimSuffix(idx, "\n"), "\n") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(destDir, entry)); err != nil {
			t.Errorf("index.txt entry %q has no file on disk: %v", entry, err)
		}
	}
}

func readFile(t *testing.T, root string, rel ...string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(append([]string{root}, rel...)...))
	if err != nil {
		t.Fatalf("read %s: %v", filepath.Join(append([]string{root}, rel...)...), err)
	}
	return string(data)
}
```

**Gate 1 CLI smoke procedure (Test Engineer; reproduce verbatim).** Observed output from the
scratch run is included so the manifest can be diffed against a known-good expectation.

```sh
go build -o /tmp/schemachecker .
W=$(mktemp -d); # write the synthetic dump above to $W/dump.sql (same content as the Go test)
/tmp/schemachecker split $W/dump.sql $W/out      # no flags
find $W/out | sed "s|$W/out|." | sort            # tree listing → evidence
cat $W/out/index.txt                             # index content → evidence
/tmp/schemachecker orphaned $W/out/index.txt; echo "exit=$?"   # must be exit=0
/tmp/schemachecker split $W/dump.sql $W/out2 --merge; echo "exit=$?"   # must be exit=99
```

Verbatim output captured on the scratch build (note the intermediate directory entries; the
two stderr INFO lines are emitted before this):

```
.
./index.txt
./orca
./orca/POLICY
./orca/POLICY/policy_drafts.sql
./orca/SEQUENCE
./orca/SEQUENCE/policy_drafts_draft_id_seq.sql
./orca/TABLE
./orca/TABLE/policy_drafts.sql
./public
./public/TABLE
./public/TABLE/orders.sql
./public/TABLE/users.sql
./SETTINGS.sql
```
(That is `find $W/out | sed "s|$W/out|.|" | sort` as captured — `SETTINGS.sql` sorts last; do not
re-sort it by hand when writing the manifest.)

Expected `index.txt` (order is split-emission order, then pruned):

```
SETTINGS.sql
public/TABLE/users.sql
public/TABLE/orders.sql
orca/TABLE/policy_drafts.sql
orca/SEQUENCE/policy_drafts_draft_id_seq.sql
orca/POLICY/policy_drafts.sql
```

Expected `public/TABLE/users.sql`:

```
CREATE TABLE public.users (
    id bigserial NOT NULL,
    email text
);
ALTER TABLE ONLY public.users ADD CONSTRAINT users_pkey PRIMARY KEY (id);

CREATE UNIQUE INDEX users_email_idx ON public.users USING btree (email);

CREATE TRIGGER users_audit AFTER UPDATE ON public.users FOR EACH ROW EXECUTE FUNCTION public.audit();
```

Expected `public/TABLE/orders.sql` contains `ALTER TABLE ONLY public.orders ADD CONSTRAINT
orders_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;` and
Expected `orca/TABLE/policy_drafts.sql` contains `draft_id bigint NOT NULL` and **no** `bigserial`.
Also capture the two stderr `INFO merging table objects` / `INFO merge complete` lines as proof the
merge ran with no flag.

**Commands the Architect ran (for reproduction).** All executed against a scratch copy
(`tar --exclude .git --exclude build --exclude dist --exclude docs`) with the change applied; the
real tree was never modified.

| Command | Key finding |
|---|---|
| `git log --oneline -6`, `git show --stat 55d5169` | `55d5169` touched `internal/split/merge_table.go` (-32/+…) and `split_test.go`; it removed the IDENTITY→bigserial folding but **kept** `inlineSequences` (`merge_table.go:206`) for `OWNED BY` sequences; `parseIdentitySequence` is gone. |
| `git show --stat ffcf7b2` | FK deferral lives in `internal/merge/merge.go` (index→migration), *not* in the split merge. |
| `grep -rn "split.Dump" --include='*.go' .` | exactly 6 sites, all in `cmd/`. No other caller. |
| `grep -rn "\bOptions\b" --include='*.go' .` | `internal/split/split.go:38,39,46` + `internal/pgdump/pgdump.go:24,25,34,197(env.Options),209` + `cmd/root.go:22` (usage text). Confirms `pgdump.Options` is unrelated. |
| `go build ./... && go vet ./...` (scratch) | green with the change. |
| `go test -race -count=1 ./...` (scratch) | **green with zero assertion edits** — see Q1. |
| `go test -race -count=1 -v ./internal/split/...` | 21/21 `--- PASS`. |
| `grep -rn "Options" internal/split` (scratch) | empty — Gate 1 grep satisfied. |
| `schemachecker split <synthetic> <dir>` | merged tree, `orphaned` exit 0 — see smoke section. |
| `schemachecker split <synthetic> <dir> --merge` | exit `99`, usage text — no flag exists. |
| `gofmt -l .` (pristine repo) | `cmd/diff.go`, `internal/merge/merge.go`, `internal/split/merge_table.go`, `internal/split/split_test.go` — pre-existing, out of scope. |

**Commit message draft (Gate 5).**

```
feat(split): always merge table-owned objects into TABLE files

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
```

## Risks / unknowns

1. **HIGH — Gate 1 smoke fixture must use two-token pg_dump headers.** Child-object filenames come
   from `reNameSplit` (`split.go:22`, applied at `:216-223`) taking the *first* token as `refName`,
   and `ResolveFilename` names `DEFAULT|TRIGGER|FK CONSTRAINT|POLICY` files after `refName`
   (`:259-266`). Real pg_dump writes `-- Name: <relation> <object>;`. If a fixture (or the new test)
   writes `-- Name: orders_user_id_fkey; Type: FK CONSTRAINT;` — the style used by the *existing*
   unit tests at `split_test.go:547` and `:27-30` — `refName` is empty and the splitter produces a
   junk file literally named `public/FK_CONSTRAINT/.sql`, which the merge cannot match by table
   name and therefore leaves in place. Observed live:
   `/tmp/sc-smoke/out/public/FK_CONSTRAINT/.sql` survived alongside a valid index entry (so the
   orphan check still passed, i.e. **the miss is silent**). Be precise about the three header
   styles: the *existing* tests use the **reversed** two-token form `<object> <relation>`
   (`split_test.go:547`: `-- Name: audit_trigger users; Type: TRIGGER;`, and `:27-30` in
   `TestResolveFilename`), which names the file after the object (`public/TRIGGER/audit_trigger.sql`)
   and is still rescued by `mergeByContent`'s `ON <table>` scan — so those pass either way. Only the
   **single-token** form produces the `.sql` junk file. Consequence for the plan: (a) Task 2's
   new test and Gate 1's smoke dump must use the two-token form for every child object; (b) do not
   read the leftover `.sql` case as a regression introduced here — it is pre-existing
   `ResolveFilename` behavior and fixing it is out of scope. Mitigation: assert the absence of the
   five child *directories* **and** `!strings.Contains(indexContent, "FK_CONSTRAINT/")` in the new
   test, both of which only hold if the fixture is realistic.
2. **MEDIUM — index.txt replay ordering for FK constraints** (see Q5.3). Mitigation: Gate 5's
   commit body states it explicitly, README:91 gains the caveat in Task 3, and the in-repo consumer
   (`merge.FromIndex`) is provably unaffected. Residual unknown: any external Taskfile/generator in
   another repo that replays `index.txt` sequentially cannot be checked from here — flag it in the
   release note rather than pretend it is verified.
3. **MEDIUM — merge failures now abort commands that previously always succeeded.** The
   `MergeTableObjects` error path was unreachable in production (every site passed
   `Options{Merge: false}`); it is now on the happy path. Any latent I/O bug in
   `inlineSequences` / `mergeSchemaTables` / `updateIndexAfterMerge` (e.g. a `TABLE/<t>.sql`
   rewritten under a read-only mount) turns into exit `3` for `split`, `dump --split`, `check` and
   `diff`. Mitigations: Task 1's reworded error (no false "unmerged copy available" promise);
   the unit tests cover the main branches; the non-Docker CI simply cannot exercise the real
   `pg_dump` shape, so `check`/`diff` against a live DB stay unverified by design (goal excludes
   Docker). If a real-schema regression appears later, it will show up as *mass diffs* inside
   `TABLE/*.sql` — not as a crash.
4. **MEDIUM — non-idempotence is now default-path behavior with an out-of-package invariant.** No
   production site can reach a stale tree (§Q3), but the guarantee is not enforced in
   `internal/split`. Mitigation: the doc-comment contract in Task 1 step 2; every test uses
   `t.TempDir()` + a never-before-used `destDir`. Explicitly rejected (see alternatives): making
   `Dump` clean `destDir` itself, which would duplicate `cmd.cleanOutputDir`'s dangerous-path
   policy in an internal package.
5. **LOW — Gate grep pitfalls for the Test Engineer.** Bare `grep -rn Options` still matches
   `pgdump.Options`, `env.Options` and `cmd/root.go:22`; those are not Gate 1 failures — scope the
   grep to `internal/split`. `task ci` does not run `gofmt`, and four files are already
   gofmt-dirty at HEAD: do not let a formatter touch `cmd/diff.go` / `merge.go` / `merge_table.go`.
6. **LOW — CI has no Docker.** Gate 3 is `build + vet + test -race`, all Docker-free (the
   `internal/pgdump` tests pass without a container). Nothing in this plan needs a DB.
7. **NOT A RISK, but note it:** the working tree already shows `?? docs/` — the as-goals artifacts
   are untracked at iteration start. Task 4's "tree clean" evidence means the *code* tree committed;
   decide explicitly whether `docs/as-goals/**` is committed or gitignored, otherwise the Delivery
   Lead will report a dirty tree at every iteration.

## Gate reachability verdict

All five gates are reachable inside the stated scope; none requires softening.

- Gate 1 — reachable; demonstrated end-to-end on a scratch build (code shape + unit test + CLI
  smoke + `orphaned` exit 0). Only risk 1 can make it *appear* to fail; use the realistic fixture.
- Gate 2 — reachable; 6/6 sites migrated, `split.Options` grep empty, `go build ./...` green.
  `cmd/dump.go` comment item is Task 3's, but Gate 2 lists it → do Task 3 before declaring Gate 2.
- Gate 3 — reachable; measured green. IDENTITY guards named, plus a new default-path IDENTITY
  assertion because the two named tests bypass `Dump`.
- Gate 4 — reachable; all stale phrases located (`cmd/split.go:10-11`, `cmd/dump.go:17-19`,
  `README.md:91` + `:102-127`). `AGENTS.md` and the rest of `README.md` contain no `--merge` or
  "no merge"/"raw pg_dump representation" claim today.
- Gate 5 — reachable; commit draft above already contains impact + rollback ("revert this commit;
  by design there is no toggle"), and neither `version.txt` nor `CHANGELOG.md` is in any task.

## Out of scope (do not plan)

`internal/merge.FromIndex`; Docker end-to-end drift verification; `version.txt` / `CHANGELOG.md`
edits; re-designing or making `MergeTableObjects` idempotent; fixing `ResolveFilename`'s empty-
`refName` → `.sql` naming; adding any flag (`--merge` or `--no-merge`); repo-wide `gofmt`;
POLICY merging; silencing the `slog.Info` merge lines.

## Three-perspective convergence (how the plan above was derived)

- **Simplicity:** delete `Options` entirely rather than leaving an empty struct; the merge call is
  three lines in `Dump`; docs are corrected in one dedicated task so the Engineer never writes
  prose under compile pressure.
- **Robustness:** keep the merge as a post-pass (content-matching requires a finished tree);
  reword the fallback error that would lie; state the fresh-`destDir` precondition in the doc
  comment since the invariant is enforced outside the package; add a default-path IDENTITY
  assertion because the existing IDENTITY tests bypass `Dump`.
- **Minimal change / risk:** measured diff is 6 files of code + 1 test file + 2 doc files; zero
  existing assertions rewritten; no new exported symbol, no new flag, no behavior in
  `MergeTableObjects`; the only net-new behavior is which code path calls it.

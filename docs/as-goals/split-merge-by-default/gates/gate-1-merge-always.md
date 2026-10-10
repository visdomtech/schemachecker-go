# Gate 1: merge-always

## Condition

`split.Dump` unconditionally performs the merge post-processing, and the merge knob does not
exist to be turned off. Concretely: `internal/split` exports no `Options` type (or any
`Merge` field/flag), `Dump`'s signature carries no merge parameter, `MergeTableObjects` runs on
every successful split, and the built CLI's `split` subcommand produces a merged tree from a
synthetic pg_dump with no flag passed.

## Evidence Required

- [ ] `internal/split/split.go` — `Dump(dumpFile, destDir string) error` with an unconditional
  `MergeTableObjects(destDir)` call → `internal/split/split.go`
- [ ] Grep proof that no `Options`/`Merge bool` symbol remains in `internal/split` → recorded in
  `evidence-manifest.md`
- [ ] A unit test asserting the **default** split path merges child objects into the TABLE file →
  `internal/split/split_test.go`
- [ ] CLI smoke run: built binary, `schemachecker split <synthetic dump> <dir>` with no flags —
  child SQL present in `TABLE/<table>.sql`; no `INDEX/`, `TRIGGER/`, `DEFAULT/`, `CONSTRAINT/`,
  `FK_CONSTRAINT/` files; `index.txt` contains no entry for a file that no longer exists →
  output captured in `evidence-manifest.md`

## Verification Method

Test Engineer builds the binary, writes a synthetic dump containing a TABLE plus an INDEX, a
TRIGGER, a CONSTRAINT and a DEFAULT (in the pg_dump `-- Name: …; Type: …; Schema: …; Owner: …`
comment form), runs `schemachecker split` against it, then lists the resulting tree and greps
`index.txt` for orphan entries. Plus `grep -rn "Options" internal/split`.

## Owner

Engineer (implementation), Test Engineer (evidence).

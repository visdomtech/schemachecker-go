# split-merge-by-default

## Goal

Merged per-object output is the only shape `schemachecker` produces: the post-split inlining
of INDEX / TRIGGER / CONSTRAINT / DEFAULT / FK_CONSTRAINT objects into the corresponding
`TABLE/*.sql` file (what the removed `--merge` flag used to request) becomes unconditional
inside `split.Dump`, with no flag in either direction.

## Context

Commit `55d5169` ("fix: preserve IDENTITY columns instead of converting to bigserial", PR #6)
deleted the `--merge` option from `cmd/split.go`. Removing the flag is correct — but the same
commit left the merge post-processing **off** at every call site, so splitting a dump silently
stopped producing the canonical merged tree.

Two unrelated mechanisms both called "merge" exist in this codebase:

- `internal/merge.FromIndex` — index file → single migration SQL. Used by `check` and the
  `merge` subcommand. **Not touched by this goal.**
- `split.Options.Merge` / `split.MergeTableObjects` — post-split inlining of child objects into
  TABLE files. **This goal is about this one.**

Current state (HEAD `b97ecd7`, tree clean):

- `internal/split/split.go:39-43` declares `Options{Merge bool}`; `Dump` at `split.go:77-81`
  calls `MergeTableObjects` only `if opts.Merge`.
- Six production call sites pass `split.Options{}` (merge OFF): `cmd/split.go:22`,
  `cmd/dump.go:76` (`dump --split`), `cmd/check.go:81` and `:93`, `cmd/diff.go:72` and `:87`.
- Six test call sites pass `Options{}` and one (`internal/split/split_test.go:558`,
  `TestDumpWithMerge`) passes `Options{Merge: true}`.
- Doc comments added by `55d5169` now assert the wrong thing: `cmd/split.go:10-11`
  ("without merge, preserving the raw pg_dump representation") and `cmd/dump.go`
  ("no merge, preserving raw pg_dump representation").
- `55d5169` also stopped folding IDENTITY sequences into `bigserial`
  (`internal/split/merge_table.go`, `inlineSequences`). That behavior is orthogonal and stays.

## Success Criteria

- `split.Dump` always runs `MergeTableObjects`; `Options` and `Options.Merge` no longer exist.
- All six production call sites and all test call sites work against the new signature, and a
  CLI smoke run of the built binary on a synthetic dump yields a merged tree: child object SQL
  present in `TABLE/<table>.sql`, no `INDEX/`, `TRIGGER/`, `DEFAULT/`, `CONSTRAINT/`,
  `FK_CONSTRAINT/` files, and no orphaned entries left in `index.txt`.
- `go build ./...`, `go vet ./...` and `go test -race` are green — including the IDENTITY
  preservation tests from `55d5169` (`TestMergePreservesIdentitySequence`,
  `TestMergeInlineIdentitySequence`).
- Documentation matches behavior: no stale claim that split output preserves the raw pg_dump
  representation.
- The commit states the output-shape behavior change clearly enough for downstream consumers of
  the released binary.

## Constraints

- No new flags and no opt-out: neither `--merge` nor `--no-merge` returns.
- IDENTITY-column preservation from `55d5169` is untouched — IDENTITY sequences must **not** be
  converted to `bigserial`. This change only re-enables object inlining.
- Verification needs no Docker: unit tests plus a CLI smoke run on a synthetic dump file.
- `check`/`diff` comparison semantics stay symmetric (both sides merge identically); no drift
  verdict changes are in scope to prove.

## Out of Scope

- `internal/merge.FromIndex` (index → single migration).
- End-to-end drift verification against a Docker-provisioned PostgreSQL and a real
  schema-index / migrations pair.
- Version bump and CHANGELOG — `release-please` owns those (`version.txt` is `1.0.1`).
- Re-designing `MergeTableObjects` itself (its non-idempotence note stays as-is).

## Confirmed

Statement of intent confirmed by user with explicit "yes" on 2026-10-10, after a 3-question
interview that fixed (a) scope = every split call site / inside `split.Dump`, (b) no escape-hatch
flag, (c) evidence bar = unit tests + CLI smoke on a synthetic dump.

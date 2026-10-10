# Gate 4: docs-consistent

## Condition

Documentation describes merged output as the behavior rather than as an option. No surviving text
claims that `split` or `dump --split` preserves the raw pg_dump representation, no `--merge` flag
is documented anywhere, and the README `split` section reflects the actual per-object output shape
(child objects inlined into `TABLE/*.sql`).

## Evidence Required

- [ ] `cmd/split.go` and `cmd/dump.go` doc comments corrected → file paths in `evidence-manifest.md`
- [ ] `internal/split` package + `Dump` comments describe the merge as unconditional
- [ ] Grep proof: no `--merge` and no "no merge" / "raw pg_dump representation" claims remain in
  `README.md`, `AGENTS.md`, or any Go doc comment → recorded in `evidence-manifest.md`
- [ ] README `split` section output-shape text matches the observed tree from Gate 1's smoke run

## Verification Method

Test Engineer greps the docs and source comments for the stale phrases and reads the README
`split` / `dump` sections against the Gate 1 smoke-run tree listing.

## Owner

Documentation Engineer.

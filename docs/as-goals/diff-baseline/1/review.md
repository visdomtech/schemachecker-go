# Review — Iteration 1

## Summary
The `diff` subcommand has been implemented in `cmd/diff.go`, registered in `main.go`, and added to the usage text in `cmd/root.go`. The implementation follows the established pattern of `cmd/check.go` closely.

## Code Quality Findings

### Critical
None.

### Warning
None.

### Suggestion
None.

### Nit / FYI
- The `copyFile` helper is defined locally in `cmd/diff.go`. If other commands need file copying, it could be extracted to a shared utility. Not actionable now.

## Code Review Details

### Correctness
- `RunDiff` validates exactly 3 args (migrationsDir, baselineFile, outputDir) — correct
- Safety check refuses dangerous output directories (/, cwd, home) — matches check command
- Output directory is cleaned and recreated — prevents stale data from partial retries
- Baseline SQL file is copied into `baselineMigrations/V1.0.0__baseline.sql` with Flyway-compatible naming
- Both sides are provisioned via `pgdump.ProvisionAndDump`, split via `split.Dump`, compared via `dirdiff.New`
- Exit codes: `ExitDiff` (1) for schema differences, `ExitInfra` (3) for infrastructure failures, `ExitUsage` (99) for bad args — all correct
- Output messages match existing patterns ("Exporting...", "The schemas are the same" / "The schemas are not the same")

### Architecture
- No new internal packages — correctly reuses existing `pgdump`, `split`, `dirdiff`, `checkererror`
- `copyFile` is a small local helper, acceptable for single-use

### Security
- Path containment via `filepath.Abs` + dangerous directory check
- No user input passed to shell commands

### Performance
- Streaming `io.Copy` for file copying — efficient for large files
- No unnecessary memory allocation

### Completeness
- All gate evidence items present: cmd/diff.go, main.go registration, root.go usage text, baseline wrapping, error handling, output messages

## Commits Reviewed
- `0777d61`: feat: add diff subcommand to compare migrations against a baseline SQL file
- `cd8580f`: fix: suppress owner statements in pg_dump output (prior cycle)
- `4f3684d`: fix: strip \restrict and \unrestrict from pg_dump output (prior cycle)
- `a84b890`: chore: add as-goal pipeline docs for diff-baseline

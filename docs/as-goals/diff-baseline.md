# Diff Baseline

## Goal
Users can verify that a single baseline SQL migration file produces the same database schema as a directory of incremental migration files, by running a new `diff` subcommand.

## Context
The project already has a `check` command that compares a schema-definition index against incremental migrations (provision → dump → split → dirdiff). The new `diff` command follows the same pipeline but takes a single baseline SQL file instead of an index. The baseline file is a flat SQL script (not a Flyway migration directory), so it must be wrapped into a temporary migration folder before provisioning.

## Success Criteria
- A `diff` subcommand exists: `diff [migrationsDir] [baselineFile] [outputDir]`
- The command provisions two separate databases: one from the incremental migrations directory, one from the baseline SQL file (wrapped into a temp migration folder)
- Both databases are dumped, split into per-object files, and compared using dirdiff
- Output reports "schemas are the same" (exit 0) or shows unified diff (exit 1)
- The `diff` command is registered in main.go and the usage text

## Constraints
- Follow existing command patterns (check, validate) for argument handling, error wrapping, and output
- Use existing internal packages (pgdump, split, dirdiff, checkererror) — no new internal packages needed
- The baseline SQL file is wrapped into a temp migration folder within the output directory

## Out of Scope
- Modifications to existing subcommands
- New internal packages or library code
- Changes to the split, merge, or dirdiff internals
- CI/CD integration or documentation beyond usage text

## Created
2026-08-14

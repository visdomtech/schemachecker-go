# Gate: Diff Command Implementation

## Condition
A `diff` subcommand exists that accepts `[migrationsDir] [baselineFile] [outputDir]`, provisions two separate databases (one from incremental migrations, one from the baseline SQL file), dumps both, splits both into per-object files, and compares them using dirdiff. The command is registered in `main.go` and appears in the usage text.

## Evidence Required
- [ ] `cmd/diff.go` exists with `RunDiff` function following the pattern of `RunCheck` → `cmd/diff.go`
- [ ] `main.go` registers the `diff` case in the switch statement → `main.go`
- [ ] Usage text in `cmd/root.go` includes `diff` → `cmd/root.go`
- [ ] Baseline SQL file is wrapped into a temp migration folder for provisioning → `cmd/diff.go`
- [ ] Error handling uses `checkererror` with appropriate exit codes → `cmd/diff.go`
- [ ] Output messages follow existing patterns (printf for progress, "schemas are the same" / diff for results) → `cmd/diff.go`

## Verification Method
1. Read `cmd/diff.go` and verify it follows the check command pattern
2. Read `main.go` and verify `diff` is registered
3. Read `cmd/root.go` and verify `diff` appears in usage text
4. Verify `go build ./...` succeeds
5. Verify `go vet ./...` succeeds

## Owner
Engineer

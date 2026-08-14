# Plan — Iteration 1

## Task 1: Create `cmd/diff.go` with `RunDiff` function

**File:** `cmd/diff.go` (new)

**Steps:**
1. Create `cmd/diff.go` following the pattern of `cmd/check.go`
2. `RunDiff(args []string) error` — validates 3 args: `migrationsDir`, `baselineFile`, `outputDir`
3. Safety check: refuse dangerous output directories (same as check)
4. Clean and recreate output directory (same as check)
5. Create `baselineMigrations` subfolder in output dir
6. Copy baseline SQL file into the subfolder as `V1.0.0__baseline.sql`
7. Provision and dump incremental migrations → `incrementalDump.sql`
8. Provision and dump baseline migrations → `baselineDump.sql`
9. Split both dumps → `incrementalsplit/` and `baselinesplit/`
10. Compare splits using `dirdiff.New`
11. Output "The schemas are the same" (exit 0) or diff + "The schemas are not the same" (exit 1)

**Acceptance criteria:**
- `RunDiff` function exists and handles all 3 arguments
- Baseline SQL file is copied into a temp migration folder with Flyway-compatible naming
- Both sides are provisioned, dumped, split, and compared
- Error handling uses `checkererror.Wrap` / `checkererror.New` with appropriate exit codes
- Output messages follow existing patterns

## Task 2: Register `diff` in `main.go` and update usage text

**File:** `main.go`, `cmd/root.go`

**Steps:**
1. Add `case "diff":` to the switch in `main.go` calling `cmd.RunDiff(args)`
2. Add `    - diff` to the usage text in `cmd/root.go`

**Acceptance criteria:**
- `schemachecker diff ...` dispatches to `RunDiff`
- Usage text lists `diff` among available commands
- `go build ./...` succeeds
- `go test ./...` succeeds
- `go vet ./...` succeeds

# Evidence Manifest — Iteration 2

## Gate Status

| Gate | Status | Evidence | Owner |
|------|--------|----------|-------|
| Gate 1: Merge Always | ✅ Pass | `internal/split/split.go:52` (Dump signature), `:83` (unconditional MergeTableObjects), grep `Options\|Merge bool` empty, 29/29 tests pass | Engineer |
| Gate 2: Call Sites Migrated | ✅ Pass | 6 `split.Dump` call sites all two-argument (`cmd/check.go:81,93`, `cmd/diff.go:72,87`, `cmd/split.go:36`, `cmd/dump.go:78`), grep `split.Options` empty | Engineer |
| Gate 3: Suite Green | ✅ Pass | `go build ./...` clean, `go vet ./...` clean, `go test -race ./...` all pass (29 split tests incl. 6 new regression tests) | Engineer |
| Gate 4: Docs Consistent | ✅ Pass | grep `--merge` empty, grep `no merge` empty, grep `raw pg_dump representation` empty; README, cmd/split.go, cmd/dump.go, internal/split/split.go all updated | Documentation Engineer |
| Gate 5: Release Impact | ✅ Pass | commits `2ffd3ab` + `5f8a2af` state behavior change clearly; `version.txt` + `CHANGELOG.md` untouched (git diff --stat empty); working tree clean | Release Engineer / Delivery Lead |

## Return Shipments (Failed Gates)

None — all gates pass.

## Code Quality Findings

- Critical: 0
- Warning: 0
- Suggestion: 0

## Commits Reviewed

- `2ffd3ab`: fix(split): anchor bigserial conversion to the owned column definition line
- `5f8a2af`: docs(split): evidence-first documentation of merge survivors and C1 fix

## Detailed Evidence

### Gate 1 — Merge Always

**Dump signature** (`internal/split/split.go:52`):
```go
func Dump(dumpFile string, destDir string) error {
```
No Options type, no Merge field, no merge parameter.

**Unconditional merge call** (`internal/split/split.go:83`):
```go
if err := MergeTableObjects(destDir); err != nil {
```
Runs on every successful split — no conditional guard.

**Grep proof** — `grep -rn "Options\|Merge bool" internal/split/`: empty (no matches).

**Unit tests**: 29/29 pass (23 pre-existing + 6 new regression tests covering sibling-ID columns,
quoted identifiers, last-column ownership, stranding guard, full Dump path, column-named-like-type-token).

### Gate 2 — Call Sites Migrated

All 6 `split.Dump` invocations use two-argument form:
```
cmd/check.go:81:  split.Dump(schemaDump, schemaSplit)
cmd/check.go:93:  split.Dump(incrementalDump, incrementalSplit)
cmd/diff.go:72:   split.Dump(incrementalDump, incrementalSplit)
cmd/diff.go:87:   split.Dump(baselineDump, baselineSplit)
cmd/split.go:36:  split.Dump(args[1], outputDir)
cmd/dump.go:78:   split.Dump(tmpPath, outputDir)
```

**Grep proof** — `grep -rn "split.Options" cmd/`: empty (no matches).

### Gate 3 — Suite Green

```
go build ./...    → clean
go vet ./...      → clean
go test -race ./... → all pass
  internal/merge   1.248s
  internal/pgdump  1.464s
  internal/split   1.254s
```

### Gate 4 — Docs Consistent

**Grep proofs** (searching README.md, AGENTS.md, cmd/, internal/):
- `--merge`: empty
- `no merge`: empty
- `raw pg_dump representation`: empty

**Updated doc comments:**
- `cmd/split.go`: comprehensive survivor list (INDEX, TRIGGER, DEFAULT, CONSTRAINT, FK_CONSTRAINT inlined; SEQUENCE inlined with identity+default+nextval; standalone COMMENT, RULE, POLICY, MATERIALIZED VIEW, FOREIGN TABLE, TABLE ATTACH, INDEX ATTACH preserved)
- `cmd/dump.go`: cross-reference to RunSplit for child objects that legitimately survive
- `internal/split/split.go`: package doc + Dump doc both state unconditional merge, with qualifier for unresolvable children
- `README.md`: evidence-based survivor descriptions, N1 caveat (creation-order not replay-order), extended survivor list

### Gate 5 — Release Impact Documented

**Commit subjects:**
- `2ffd3ab`: `fix(split): anchor bigserial conversion to the owned column definition line`
- `5f8a2af`: `docs(split): evidence-first documentation of merge survivors and C1 fix`

**version.txt / CHANGELOG.md untouched:**
`git diff --stat 30c2a8b..HEAD -- version.txt CHANGELOG.md` → empty output.

**Working tree:** `git status --short` → empty (clean).

**Rollback path:** revert the two commits — by design there is no toggle to add back.

# Gate 2: call-sites-migrated

## Condition

Every caller of `split.Dump` compiles against the new signature and none passes a merge option.
The six production sites — `cmd/split.go`, `cmd/dump.go` (`dump --split`), `cmd/check.go` (both
sides), `cmd/diff.go` (both sides) — plus every test call site are updated. `check` and `diff`
still split **both** sides, so their comparison remains symmetric; no call site silently keeps
unmerged behavior.

## Evidence Required

- [ ] All six production call sites listed above → file:line in `evidence-manifest.md`
- [ ] Grep proof: zero `split.Options` occurrences repo-wide → recorded in `evidence-manifest.md`
- [ ] `cmd/dump.go` doc comment no longer describes a "no merge" mode for `dump --split`
- [ ] `go build ./...` succeeds → recorded in `evidence-manifest.md`

## Verification Method

Test Engineer runs `grep -rn "split.Dump" --include=*.go .` and confirms each hit uses the
two-argument form, then `grep -rn "split.Options" .` returns nothing, then builds.

## Owner

Engineer.

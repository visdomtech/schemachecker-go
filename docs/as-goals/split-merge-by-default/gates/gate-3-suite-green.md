# Gate 3: suite-green

## Condition

The full verification suite passes on the final tree state, not just on gate-linked evidence, and
the IDENTITY-column preservation introduced by `55d5169` still holds under always-merge. Namely:
`go build ./...`, `go vet ./...`, and `go test -race -count=1 ./...` (equivalently `task ci`) are
all green, with `TestMergePreservesIdentitySequence` and `TestMergeInlineIdentitySequence`
passing — IDENTITY sequences must not become `bigserial` and their `SEQUENCE/` files must survive
the merge.

## Evidence Required

- [ ] `go build ./...` output → captured in `evidence-manifest.md`
- [ ] `go vet ./...` output → captured in `evidence-manifest.md`
- [ ] `go test -race -count=1 ./...` full run, no failures → captured in `evidence-manifest.md`
- [ ] Named evidence that the two IDENTITY tests pass → test output lines in `evidence-manifest.md`

## Verification Method

Test Engineer runs the commands personally on the committed tree and records verbatim results.
Self-reported passes from the Engineer are not accepted as evidence.

## Owner

Engineer (make it pass), Test Engineer (verify it passed).

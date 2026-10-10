# Gate 5: release-impact-documented

## Condition

The change is shippable as-is: the commit message states the observable behavior change clearly
enough for a downstream consumer reading a release note (split output is always merged; the
type-partitioned `INDEX/`, `TRIGGER/`, `DEFAULT/`, `CONSTRAINT/`, `FK_CONSTRAINT/` directories are
no longer emitted), a rollback path exists and is stated (revert the commit — by design there is
no toggle to add), and nothing hand-edited `version.txt` or `CHANGELOG.md` because release-please
derives them.

## Evidence Required

- [ ] Commit subject/body verbatim → `evidence-manifest.md`
- [ ] Impact + rollback paragraph → Release Engineer note in `review.md` or the final report
- [ ] `git diff --stat` proof that `version.txt` and `CHANGELOG.md` are untouched by this change
- [ ] Working tree clean, all work committed

## Verification Method

Release Engineer reads `git log -1 --stat` for the iteration's commit and checks the two files are
unmodified; Delivery Lead confirms `git status` is clean.

## Owner

Release Engineer (bench), Delivery Lead (clean-tree check).

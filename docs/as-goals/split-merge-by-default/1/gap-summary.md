# Gap Summary — Iteration 1

**Decision:** LOOP → iteration 2. **Gates:** 4/5 Pass, 1 Fail. **Blocking for DONE:** Gate 4 Fail +
unresolved Critical C1.
**Manifest validity:** pre-check passed — all five gates carry a Pass/Fail verdict with linked,
self-produced evidence (`1/evidence-manifest.md`). No Engineer or Architect self-report was accepted.

## Failed Gates

- **Gate 4 — docs-consistent.** README.md:153-156 and the `e886939` commit body state in absolute
  terms that `INDEX/` and `TRIGGER/` are *never* emitted after merging. Real `pg_dump` output
  disproves that: matview indexes, `INSTEAD OF` triggers on views, and quoted relations
  (`ON public."Weird Name"`) legitimately survive as separate files because they have no owning
  TABLE file to merge into. → routed to **Documentation Engineer** (with the Engineer for the
  commit-body correction). **Priority: Warning** (it fails a gate, so it blocks DONE regardless).

## Critical (blocks DONE hygiene)

- **C1 — always-merge fabricates `bigserial` on any column whose name *ends with* an owned
  sequence's column name.** `internal/split/merge_table.go:337-338` builds `reCol` from
  `columnName` with no left boundary and no line anchor, and `:342` applies `ReplaceAllString` to
  the whole file, so `OWNED BY foo.id` also rewrites `user_id bigint NOT NULL`, `external_id …`,
  `workspace_id …` into `bigserial`. Verified against live PostgreSQL 17 by the Test Engineer:
  split → `merge` → load into a fresh DB yields `user_id | bigint | not null |
  nextval('orders_user_id_seq'::regclass)` — a sequence the source schema never had.
  - **Root cause:** the defect predates this work, but `55d5169` left the merge off at every call
    site, so the released 1.0.1 binary never executed it. Making the merge unconditional moves a
    dormant defect onto the happy path of `split`, `dump --split`, `check` and `diff`. Proven by
    building `HEAD~2` and splitting the same real dump: the old binary leaves `user_id bigint`.
  - **Routed to:** **Engineer** (implementation defect, not an architectural one).
  - **Priority: Critical.**
  - **Scope note:** fixing the anchoring is *not* the "re-designing `MergeTableObjects`" excluded
    by the goal — it is the minimum correctness work for the path this goal makes default. No new
    gate is added and no existing gate is softened.

## Regressions

- None. This is iteration 1, so no previously-passed gate could flip.

## Unresolved Findings (survived review's own fix pass)

- **Critical:** C1 (above).
- **Warning W1:** the Gate 4 docs overclaim (above).
- **Warning W2 — DEFERRED, needs a user decision, deliberately NOT in iteration 2:** the merge
  post-pass is O(tables × index/trigger files) because `mergeByContent` re-reads every file in the
  directory per table (`merge_table.go:119-146`), and it is now paid by every command. Measured
  4.48 s vs 0.28 s (16×) at 300 tables × 900 indexes; minutes at 1000 × 5000. Not a gate failure
  and not a Critical, and fixing it is a refactor of `MergeTableObjects` — which the goal places
  out of scope. Recorded for the user rather than silently absorbed into this change.
- **Suggestion S1:** `Dump`'s "destDir must be empty or non-existent" precondition is documented
  but unenforced (the invariant lives in `cmd.cleanOutputDir`).
- **Suggestion S2:** README.md:105 advertises a dangerous-path refusal that is bypassable under a
  symlinked cwd — pre-existing, unrelated to merge.

## Next Iteration Focus (iteration 2)

- [ ] Engineer: anchor `reCol` to a column-definition position, require a full-token identifier
      match rather than a substring, and replace only the matched column line — with a regression
      test in the `foo(id, external_id, user_id, width)` shape (no existing `TestMergeInlineSequence*`
      fixture has an `*_id` sibling column, which is why 23/23 tests were green over this bug).
- [ ] Test Engineer must re-prove C1's fix against a **real** dump (the reviewer already has a
      live-DB harness) — unit tests alone did not catch it.
- [ ] Documentation Engineer: replace the absolute "never emitted" claims with what is actually
      true (which child objects merge, which survive and why), and correct the record on the
      commit-body claim in a follow-up commit rather than by rewriting `e886939`.
- [ ] Re-evaluate **all five** gates, including 1, 2, 3 and 5, which passed iteration 1 — the C1
      fix touches `MergeTableObjects`' output, so Gate 1 and Gate 3 are at regression risk.

# Task Card — Phase 3: Embeddings, related notes, merge

Read order: this card, `../IMPLEMENTATION-FRAMEWORK.md`, then `../DESIGN.md`
sections 3.3, 5.6, 5.8, 7.5, 9.

Prerequisite: phase 2 done and green; read its final report first.

## Goal

Every note carries an embedding in the index, notes can be queried for
related notes, and duplicates can be merged.

## Build

1. **embed pipeline.** embeddings adapter (DESIGN.md 7.5), the `embed` chain
   step completing every chain, vector stored in the index with model name.
2. **Re-embedding on content change**: append, entry_edit, merge re-enter
   the chain at `embed` (never at `tag`, DESIGN.md 8).
3. **GET /v1/notes/:id/related** (5.6): application-side cosine over BLOB
   vectors, top 10, config threshold, 503 before the first embedding.
4. **POST /v1/notes/:id/merge** (5.8): entries in timestamp order, others to
   trash, dedup events, one commit.
5. **Dedup hardening**: content_hash computed at ingestion and on merge;
   near-duplicate detection is NOT in scope (that is what related is for);
   ensure `resaved` bumps also re-embed if the bump changed content.
6. **stub_embed** per the framework (deterministic vector from text hash).

## Not in this phase

Reminders surfacing, GC, push cron, any UI.

## Acceptance script (sketch, tighten at phase start)

```bash
# after creating several notes: related returns the thematically closest
#   note first (assert with crafted stub vectors)
# embedding updates after an append (updated_at changes)
# merge: A and B merge into A, B in .trash, entries interleaved by ts,
#   one commit "merge <A> <B>"
# related before any embedding -> 503
```

## Definition of done

Framework 8, plus: re-embedding demonstrated for append and for entry_edit.

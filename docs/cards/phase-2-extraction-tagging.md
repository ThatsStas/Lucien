# Task Card — Phase 2: Link/YouTube extraction and LLM tagging

Read order: this card, `../IMPLEMENTATION-FRAMEWORK.md`, then `../DESIGN.md`
sections 4, 5.1, 5.2, 7.2, 7.3, 7.4, 8.

Prerequisite: phase 1 done and green. Read its final report before starting;
it lists the design gaps found, which may amend this card.

## Goal

Notes created from a URL gain their content automatically, and every note
gets agent tags at ingestion.

## Build

1. **Link extraction.** trafilatura helper subprocess contract (DESIGN.md
   7.2), the adapter, the `extract` chain step for `source.kind: link`,
   excerpt entry on success, permanent failure on empty extraction.
2. **YouTube extraction.** yt-dlp adapter (7.3): `--skip-download
   --write-auto-subs`, VTT to plain text, `transcript` entry, video title
   into the note. Cookie-expired permanent failure with the exact
   `last_error` wording from DESIGN.md.
3. **URL classification** in POST /v1/notes: youtube hosts vs generic link.
4. **Tagging.** llm adapter (7.4), the `tag` chain step, the merge rule
   (add `by: agent`, case-insensitive dedupe, never remove). Tagging prompt
   lives in config.
5. **POST /v1/notes/:id/retag**: clears `by: agent` tags, re-enters the
   chain at `tag`. 409 unless the note is at rest (`step: none` or failed).
6. **POST /v1/ingest** (ribbon shape, DESIGN.md 5.2).
7. **FTS `q`**: switch the query filter from LIKE to the FTS5 table if phase
   1 shipped the LIKE interim.
8. **Stubs**: shim_yt-dlp, shim_extract, stub_llm per the framework.

## Not in this phase

Embeddings, related, merge, dedup behavior beyond what phase 1 already
shipped (URL hash + resaved events), reminders, GC.

## Acceptance script (sketch, tighten at phase start)

```bash
# link note gains an excerpt entry and agent tags, chain reaches none
ID=$(curl -s -X POST $B/v1/notes -H "Authorization: Bearer $T" \
  -d '{"url":"https://example.com/article"}' ... | jq -r .id)
# poll: entries has kind=excerpt, tags has by=agent entries,
#       processing.step == "none"

# youtube note gains a transcript entry
# dedup: POST the same URL again -> 200 with deduped:true, resaved[] grew
# expired-cookie youtube failure -> processing.status=failed with the
#   documented last_error; POST /retry after fixing shim -> completes
# retag: PATCH human tag onto the note, POST retag -> agent tags replaced,
#   human tag intact
# ingest: POST ribbon shape -> note born with excerpt, chain starts at tag
# q: full-text query finds a word inside the excerpt
```

## Definition of done

Framework 8, plus: every adapter failure mode (retryable, permanent,
malformed LLM output) demonstrated in a system test.

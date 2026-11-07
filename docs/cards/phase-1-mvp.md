# Task Card — Phase 1: MVP

Entry point for the implementing model. Read order:

1. This card, fully.
2. `../IMPLEMENTATION-FRAMEWORK.md`, fully.
3. `../DESIGN.md` sections 0, 1, 2, 3, 4, 5 (5.1, 5.4, 5.5, 5.7, 5.10, 5.11),
   6, 7.1, 9, 10, 11, 12.

Skip the rest of DESIGN.md; later phases cover it.

## Goal

A working backend that creates, stores, edits, deletes and transcribes notes,
with git-versioned storage and the asynchronous worker spine proven end to
end. After this phase everything else is a new chain step or a new endpoint,
never new architecture.

## Build

1. **Scaffold + config.** Binary skeleton, typed config loader (DESIGN.md 12:
   tokens, notes_dir, bind, port, whisper_url, backoff values,
   trash_retention_days, TLS fields present but unused). First test: config
   validation rejects a missing required field.
2. **Note Store.** Schema validation, atomic write (temp + rename), ULID
   generation, create/read/append/edit/delete, dedup hash computation, git
   commit per content mutation behind a VCS interface. Seam tests against a
   temp dir with null_git, then with real git.
3. **Index.** SQLite schema from DESIGN.md 9, transactional update on every
   Note Store mutation, `reindex` from files. Note: phase 1 needs the filter
   columns only; the FTS table exists but `q` may do a plain LIKE until
   phase 2.
4. **API layer.** `/v1/health`, bearer auth (401, constant-time compare),
   POST /v1/notes (text + multipart audio), GET /v1/notes (state, tag,
   min_age, max_age, modified_since, processing_status, limit, offset),
   GET /v1/notes/:id, PATCH /v1/notes/:id (409 semantics), DELETE
   /v1/notes/:id, POST /v1/admin/reindex.
5. **Worker + transcribe pipeline.** Chain runner (discovery query, signal +
   deadline wake-ups, boot reset of `running`), the whisper adapter, backoff
   per DESIGN.md 4, audio deletion on success.
6. **Stubs.** stub_whisper and null_git per the framework.

## Explicitly not in this phase

Links, YouTube, tagging, embeddings, related, merge, retag, ribbon ingest,
reminders surfacing, trash GC, push cron. No code "preparing" for them
beyond what DESIGN.md already specifies (the chain table shape, the schema).

## Acceptance script

Run in a clean checkout with `B` = base URL of the test instance,
`T` = a valid token, stub_whisper running.

```bash
# 1. text note
ID1=$(curl -s -X POST $B/v1/notes -H "Authorization: Bearer $T" \
  -H 'Content-Type: application/json' -d '{"text":"buy milk"}' | jq -r .id)
test -f "$NOTES_DIR/$ID1.json"                                  # file exists
jq -r .state "$NOTES_DIR/$ID1.json"                             # -> inbox
git -C "$NOTES_DIR" log --oneline | grep "add note $ID1"        # one commit

# 2. audio note transcribes end to end
ID2=$(curl -s -X POST $B/v1/notes -H "Authorization: Bearer $T" \
  -F file=@tests/fixtures/sample.m4a | jq -r .id)
jq -r .processing.step "$NOTES_DIR/$ID2.json"                   # -> transcribe
# poll (bounded, no sleep-loop in the test harness):
curl -s $B/v1/notes/$ID2 -H "Authorization: Bearer $T" | \
  jq -e '.entries[] | select(.kind=="transcript")'              # lands within 30s
test ! -f "$NOTES_DIR/$ID2.1.m4a"                               # audio deleted
git -C "$NOTES_DIR" log --oneline | grep "transcribe $ID2"      # one commit

# 3. whisper outage -> retry, then boot recovery
#    (stop stub_whisper, POST another audio note, observe retry_count>0,
#     restart the backend, start stub_whisper, transcript lands)

# 4. concurrency
MOD=$(curl -s $B/v1/notes/$ID1 -H "Authorization: Bearer $T" | jq -r .modified)
curl -s -o /dev/null -w '%{http_code}' -X PATCH $B/v1/notes/$ID1 \
  -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"base_modified":"1970-01-01T00:00:00Z","state":"todo"}'  # -> 409
curl -s -X PATCH $B/v1/notes/$ID1 -H "Authorization: Bearer $T" \
  -H 'Content-Type: application/json' \
  -d "{\"base_modified\":\"$MOD\",\"state\":\"todo\"}" | jq -r .state  # -> todo

# 5. filters and sync cursor
curl -s "$B/v1/notes?state=todo" -H "Authorization: Bearer $T" | jq -e ".notes[].id == \"$ID1\""
curl -s "$B/v1/notes?modified_since=<t0>" ...                   # only changed notes

# 6. delete and auth
curl -s -o /dev/null -w '%{http_code}' $B/v1/notes              # -> 401
curl -s -X DELETE $B/v1/notes/$ID1 -H "Authorization: Bearer $T"
test -f "$NOTES_DIR/.trash/$ID1.json"                           # moved, not gone
git -C "$NOTES_DIR" log --oneline | grep "delete $ID1"

# 7. no bookkeeping commits
git -C "$NOTES_DIR" log --oneline | grep -c "retry"             # -> 0
```

## Definition of done

Per IMPLEMENTATION-FRAMEWORK.md 8, plus: the boot-recovery case (script 3)
demonstrated with the backend killed mid-transcription.

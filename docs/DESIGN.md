# Notes Backend — Design

Version 1.0, 2026-11-07. Status: approved for implementation.

This document is the normative reference. When an implementation detail is
ambiguous or two readings are possible, this document settles it. It is written
language-independently: it specifies behavior, contracts and failure modes, not
code. The implementation language is chosen at implementation start and must
satisfy these contracts.

## 0. Scope

In scope: a self-hosted REST backend that stores notes as self-contained JSON
files, processes them through asynchronous pipelines (transcription, link
extraction, tagging, embedding), exposes a query API for frontends, and keeps
every note safe through git versioning.

Out of scope: any frontend (Android, web, agent UI), WebDAV, TodoTracker
modification or data migration, TLS termination, multi-user support, push
notifications. The backend is passive: it never initiates contact with a
client. Clients poll.

## 1. Architecture in one rule

Data moves in one direction through two independent halves.

- The API half handles requests synchronously: validate, write the note file,
  commit, respond.
- The worker half handles everything slow or fallible: transcription,
  extraction, tagging, embedding.

The halves never call each other and share no memory. They communicate only
through note files on disk. The note file is the queue, the job record and the
truth. Every restart or outage reduces to one recovery: the worker scans for
pending notes and resumes.

```
phone / web / agent / ribbon
        |  HTTP + bearer token
        v
+-------------+    every mutation   +------------+   commit per   +------+
|  API layer  | ------------------> | Note Store | -------------> | git  |
|  (routing,  |                     | (the ONLY  |   content      |      |
|   auth,     | <-----------------  |  writer of |                | push |
|   validate) |   single-note reads |  .json)    |                | cron |
+-------------+                     +------------+                +------+
      |  list queries                   ^   ^
      v                        notifies |   | scans pending via index,
+-------------+                         |   | writes step results
|   Index     | ------------------------+   |
|  (SQLite,   |                      +------------+
|   derived,  |                      |   Worker   |
|   rebuild)  |                      | (chain     |
+-------------+                      |  runner)   |
                                     +------------+
                                          | each pipeline step calls
                                          v
                              +-------------------------+
                              | Adapters: whisper, llm, |
                              | embeddings, yt-dlp,     |
                              | trafilatura             |
                              +-------------------------+
```

## 2. Modules

Six modules, one responsibility each. A module may be a library, a package or
a set of files; the boundary is what matters.

| Module | Responsibility | Must not |
|---|---|---|
| API layer | routing, auth, request validation, concurrency checks | know pipeline internals |
| Note Store | the only reader/writer of note files; schema validation, atomic writes, dedup check, index notification, git commit | be bypassed by any other module |
| Index | derived SQLite cache: filters, full-text, dedup hashes, embedding vectors | ever be treated as truth |
| Worker (chain runner) | find actionable notes via the index, run the next chain step, apply backoff | touch note files except through Note Store |
| Adapters | one per external service; hide endpoint, credentials, timeouts; classify failures retryable or permanent | be called from the API layer |
| Config | load and validate one typed config file; inject into constructors | be re-read ad hoc at runtime |

Corruption bugs have one home (Note Store). Integration bugs have one home per
service (its adapter). Sequencing bugs have one home (the chain table).

## 3. Data model

### 3.1 Storage layout

```
notes/
  <ulid>.json            one file per note, the truth
  <ulid>.<n>.m4a         transient audio buffers, deleted after transcription
  .trash/
    <ulid>.json          deleted notes, GC'd after 30 days
index.sqlite             derived cache, deletable, rebuilt by reindex
```

Flat directory. ULID: 26-character Crockford base32, time-sortable, generated
by the backend. IDs are never client-supplied.

Audio files use the note id as prefix plus a counter per append
(`<ulid>.1.m4a`, `<ulid>.2.m4a`). Accepted formats: m4a, mp3, wav, ogg, flac.
Max upload size is a config value (default 50 MB).

### 3.2 Note schema

One JSON object per note. All timestamps ISO 8601 UTC.

```json
{
  "id": "01JZK4QW8V0F3M2N1P8R7T6X5Y",
  "created": "2026-11-07T14:00:00Z",
  "modified": "2026-11-07T14:00:00Z",
  "state": "inbox",
  "state_changed_at": "2026-11-07T14:00:00Z",
  "tags": [{"name": "kernel", "by": "agent"}, {"name": "idea", "by": "human"}],
  "priority": 0,
  "source": {"kind": "audio", "url": null},
  "entries": [
    {"id": 1, "ts": "2026-11-07T14:00:00Z", "kind": "transcript", "text": "..."}
  ],
  "processing": {
    "step": "transcribe",
    "status": "pending",
    "retry_count": 0,
    "next_retry_at": null,
    "last_error": null
  },
  "remind_at": null,
  "defer_until": null,
  "dedup": {
    "url_hash": null,
    "content_hash": "sha256:...",
    "resaved": []
  }
}
```

Field rules:

- `state`: one of `inbox, todo, in_progress, done, someday, archived`.
  Transitions are permissive (any to any) via PATCH only. The backend validates
  membership, nothing more. GTD discipline is a frontend concern.
- `tags[].by`: `agent` or `human`. The tagging pipeline adds `agent` tags and
  never removes or rewrites `human` tags. Tagging runs once, at ingestion.
- `priority`: integer, -20 to 19, default 0.
- `source.kind`: `text, audio, link, youtube, ribbon`. `url` set for link and
  youtube.
- `entries[]`: the note body as an ordered list of sections. Append means
  adding an element, never rewriting an existing one. `kind` is
  `text, transcript` or `excerpt` and records provenance. `id` is a per-note
  incrementing integer. Reading the note body means concatenating entries in
  order. Editing content means replacing one entry by id (PATCH).
- `processing`: the pipeline position, see section 4. Written by the worker
  and Note Store. Clients may read it and filter on it, never set it.
- `remind_at`: notify me at this time. Distinct from `defer_until`, which
  hides the note until a date. Both are plain fields; the backend only stores
  and filters them. Clients poll and act.
- `dedup.resaved[]`: timestamps of re-saves of the same URL.
- There is no `related` field. Related notes are derived from embeddings and
  live only in the index (section 9).

### 3.3 What is not in the file

Embeddings, full-text index content, related-note similarity. All derived, all
rebuildable, all in the index. The JSON files are the only truth.

## 4. The processing state machine

Each note carries one chain position. The chain is fixed per source kind:

| source | chain |
|---|---|
| audio | transcribe -> tag -> embed -> none |
| link | extract -> tag -> embed -> none |
| youtube | extract -> tag -> embed -> none |
| text | tag -> embed -> none |
| ribbon | tag -> embed -> none |

Statuses per step:

- `pending`: due when `next_retry_at` is null or <= now.
- `running`: claimed by the worker. On backend boot, every `running` resets to
  `pending` (a crash mid-step is possible at any time).
- `failed`: permanent failure or retry cap reached. Chain stops. Visible via
  `processing_status=failed` filter. `POST /notes/:id/retry` resets to
  `pending` with `retry_count` zeroed.
- On success: advance to the next chain step as `pending`. Last step sets
  `step: none, status: pending` equivalent rest state (`step: "none"`).

Backoff: `next_retry_at = now + min(60s * 2^retry_count, 3600s)`,
`retry_count` increments per retryable failure, cap 10 retries, then `failed`.
Base, factor, cap and max retries are config values.

Idempotency rule: a pipeline step computes its full output and Note Store
applies it as one atomic mutation at the end of the step. A step never
half-writes. Re-running a crashed step is indistinguishable from running it
once.

Extensibility: a new pipeline is a new entry in the chain table plus one
adapter. No other module changes.

## 5. HTTP API contract

Base path `/v1`. Auth on every endpoint except `/v1/health`:
header `Authorization: Bearer <token>`. Config holds per-client tokens as
SHA-256 hashes (`clients: [{name, token_sha256}]`); comparison is
constant-time. Missing or invalid token: 401, no other information leaked.

All list and single-note responses carry the note JSON. List responses carry
a summary projection: `id, state, tags, priority, created, modified,
source.kind, processing.step, processing.status, first entry snippet
(200 chars)`.

### 5.1 POST /v1/notes

The single entry point for new notes. `Content-Type: application/json` for
text and links, `multipart/form-data` for audio.

Request (JSON): `{text}` or `{url}`; optional `tags`, `state` (default
`inbox`), `priority`, `remind_at`, `defer_until`.
Request (multipart): field `file` (the audio), optional `tags` etc. as form
fields.

Behavior:
1. Validate. Errors: 400 (missing text/url/file, bad field), 413 (file too
   large), 415 (unsupported audio format).
2. For `url`: classify youtube vs link by host. Compute `url_hash`
   (URL normalized: lowercase host, strip utm_* params and trailing slash,
   SHA-256) and check the index.
   - Duplicate: append now to the existing note's `dedup.resaved`, move it to
     `inbox` if it was `archived`, commit `resave <id>`, respond
     `200 {id, "deduped": true}`. No new note.
3. Create the note: ULID, timestamps, initial chain position per section 4
   (text/link: first chain step pending; audio: `transcribe/pending` and the
   audio file written beside the JSON). Atomic write, index update, git commit
   `add note <id>`.
4. Respond `201 {id}`.

Side effects: note file, audio file if present, index row, one git commit.
Failure leaves no partial state (temp file plus rename).

### 5.2 POST /v1/ingest

Ribbon's existing POST shape: `{url, title, markdown}`. Creates a note with
`source.kind: ribbon`, one `excerpt` entry from the markdown, chain starting
at `tag`. Dedup applies as in 5.1. Response as in 5.1.

### 5.3 POST /v1/notes/:id/append

JSON `{text}` or multipart `file`. Adds an entry (kind `text` now; kind
`transcript` after the audio pipeline completes). Audio append stores the file
as `<id>.<n>.<ext>` and re-enters the chain at `transcribe`. Text append
re-enters at `embed` only (no re-tagging, section 8). Commit `append <id>`.
404 unknown id, 409 if a concurrent append is mid-transcription for the same
note (client retries after it lands; keeps entry ordering unambiguous).

### 5.4 GET /v1/notes

Query params, all optional and AND-combined:
`state` (repeatable), `tag` (repeatable), `q` (full-text over entries),
`min_age`, `max_age` (durations, e.g. `7d`, measured on `created`),
`modified_since` (timestamp; the sync cursor for caching clients),
`remind_due=true` (`remind_at <= now`, state not in done/archived),
`defer_due=true` (`defer_until <= now`),
`processing_status` (`pending|running|failed`),
`limit` (default 100, max 1000), `offset`.

Reads come from the index. Response: `200 {notes: [summary...], total}`.

### 5.5 GET /v1/notes/:id

Reads the note file (truth), never the index. 404 unknown id.

### 5.6 GET /v1/notes/:id/related  (phase 3)

Cosine similarity over index vectors. `200 {related: [{id, score}...]}`, top
10, score threshold from config. 404 unknown id, 503 if no embedding yet.

### 5.7 PATCH /v1/notes/:id

Request: any subset of `state, tags, priority, defer_until, remind_at,
entry_edit: {id, text}`. Mandatory `base_modified`: the `modified` value the
client last saw.

- `base_modified` != current `modified`: 409 plus the full current note.
  The client merges or re-asks the user. Never last-write-wins.
- Valid: apply, bump `modified`, set `state_changed_at` on state change,
  index update, commit `edit <id>` (or `state <id> <new>`).
- Tag edits replace the full tag list as sent; the pipeline's once-only rule
  means no agent write ever races this.
- `entry_edit` on a note with pending transcription: 409 (text is about to
  change underneath the edit).
- 400 unknown state value, 404 unknown id.

### 5.8 POST /v1/notes/:id/merge  (phase 3)

Request: `{other_ids: [...]}`. Entries of the others append to this note in
timestamp order, the others move to `.trash/`, dedup events recorded, commit
`merge <id> <others>`. 404 any unknown id.

### 5.9 POST /v1/notes/:id/retry

Only valid on `processing.status=failed`. Resets the failed step to `pending`,
zeroes `retry_count`, clears `last_error`. 409 on any other status.

### 5.10 DELETE /v1/notes/:id

Moves the note file (and any audio buffers) to `.trash/`, updates index,
commit `delete <id>`. Recoverable by moving the file back until the GC removes
it after `trash_retention_days` (default 30). Git history retains everything
regardless. 404 unknown id.

### 5.11 Service endpoints

`GET /v1/health`: 200, no auth. `POST /v1/admin/reindex`: rebuilds the index
from the files, returns `{reindexed: <count>}`. Auth required.

## 6. The worker

One loop in the backend process. Three rules:

1. Discovery is one indexed SQL query, never a file scan:

   ```sql
   SELECT id FROM notes
   WHERE processing_status = 'pending' AND (next_retry_at IS NULL OR next_retry_at <= :now)
   ORDER BY created LIMIT 32;
   ```

2. Wake-ups are event-driven. The Note Store signals the worker in-process on
   every mutation that creates or re-arms a pending note. Between signals the
   worker sleeps until the earliest future `next_retry_at`. A lost signal
   degrades to a delayed retry, never a stuck note.

3. Claims are per note: set `running` in the file (no commit), execute the
   step, apply the result through Note Store. On boot, all `running` notes
   reset to `pending` before the loop starts.

The worker processes notes oldest-first within a batch.

## 7. Pipelines and adapters

Each adapter is a small module with one contract: typed input, typed output,
and a failure classified `retryable` or `permanent`. Pipelines contain no HTTP
or subprocess code.

### 7.1 transcribe (whisper adapter)

Input: audio file path. Action: POST the file to the configured whisper
endpoint (`whisper_url`), multipart. Output: transcript text.
Retryable: connection error, timeout, 5xx. Permanent: 4xx, unreadable audio.
On success the pipeline appends a `transcript` entry and deletes the audio
file. Language parameter from config (`whisper_language`, default `auto`).

### 7.2 extract, link kind (trafilatura adapter)

Input: URL. Action: run the trafilatura helper subprocess (stdin: URL;
stdout: JSON `{title, text}`; exit 0 success, 2 permanent, 3 retryable).
Output becomes an `excerpt` entry. Permanent failure: empty extraction,
unreachable page after helper-level retries.

### 7.3 extract, youtube kind (yt-dlp adapter)

Input: URL. Action: `yt-dlp --skip-download --write-auto-subs --sub-langs
en.*,de.* --cookies <cookies_path> <url>`, parse the VTT into plain text.
Output becomes a `transcript` entry plus the video title.
Retryable: network errors. Permanent: no subtitles, expired cookies
(`last_error` says exactly that; the operator re-exports cookies.txt from
Firefox and calls `/retry`). Playwright with an injected cookie is the
documented fallback if YouTube hardens the timedtext path; it is not built in
v1.

### 7.4 tag (llm adapter)

Input: concatenated entry text. Action: POST to `llm_url` with the tagging
prompt (system prompt in config, editable). Output: JSON tag list; malformed
output is retryable up to the cap, then failed. Merge rule: add as
`by: agent`, dedupe case-insensitively against existing tags, never remove
anything. Runs once per note. `POST /notes/:id/retag` (phase 2) clears agent
tags and re-runs.

### 7.5 embed (embeddings adapter)

Input: concatenated entry text. Action: POST to `embed_url`. Output: float
vector, stored only in the index with the model name and timestamp. Re-runs on
every content change (append, entry_edit, merge).

## 8. Tagging and re-processing policy

Tagging runs once, at ingestion. After that, tags belong to the user. Content
edits refresh the embedding (cheap, invisible) but never re-trigger tagging.
This is what makes PATCH tag edits race-free.

## 9. The index

SQLite, one file, derived. Tables:

- `notes(id, state, created, modified, priority, source_kind, url_hash,
  content_hash, processing_step, processing_status, next_retry_at, remind_at,
  defer_until)` with indexes on (processing_status, next_retry_at), state,
  modified, remind_at, defer_until.
- `tags(note_id, tag)`.
- `entries_fts`: FTS5 virtual table over (note_id, entry_id, text).
- `embeddings(note_id PRIMARY KEY, vector BLOB, model, updated_at)`.

The Note Store applies every mutation to the index transactionally with the
file write. `reindex` drops and rebuilds all tables from the JSON files.
Cosine similarity for `related` is computed application-side over the BLOB
vectors; at this scale (tens of thousands of notes) that is milliseconds.

## 10. Git

The notes directory is its own git repository, separate from the backend
source. The remote URL is a config value and otherwise irrelevant to the
design.

Commit per content mutation: `add note <id>`, `append <id>`, `transcribe <id>`,
`tag <id>`, `edit <id>`, `state <id> <new>`, `merge <id> <others>`,
`delete <id>`, `resave <id>`. Each commit names the note id, so
`git log --grep <id>` reconstructs any note's life.

Not committed: retry bookkeeping, `running` claims, embedding writes. These
write the file (restart safety) and ride along in the next content commit.
The git log reads as a history of content, not a history of retries.

Push: every 15 minutes via the host's cron or a timer in the backend
(`git -C <notes_dir> push`), plus on clean shutdown. Push failures are logged
and retried at the next interval; they never block note writes.

## 11. Failure and recovery table

| event | outcome |
|---|---|
| crash before Note Store write | nothing persisted; client sees a connection error and retries |
| file written, commit not done | content safe in the file; the next mutation or a periodic sweep commits it |
| crash mid-pipeline-step | boot resets `running` to `pending`; the step re-runs idempotently |
| whisper/llm/embeddings endpoint down | retryable backoff; notes accumulate as pending; no data loss |
| youtube cookies expired | permanent failure with a clear `last_error`; operator re-exports and retries |
| index corrupted or deleted | `POST /v1/admin/reindex` rebuilds from the files |
| push remote unreachable | commits accumulate locally; push retries every interval |
| two clients edit one note | second writer gets 409 plus the current note |

## 12. Security

Single user. Bearer tokens per client, stored as SHA-256 hashes in the config,
compared in constant time. LAN/VPN deployment in v1: the backend binds to an
interface and port from config, no TLS (the VPN provides transport security).
The config schema includes TLS cert/key fields from day one so exposure later
is a config change, not a code change. Tokens are per client (phone, web,
agent, ribbon), revocable individually.

## 13. Testing strategy

Defined in IMPLEMENTATION-FRAMEWORK.md. Unit tests per module, system tests as
real curl flows against the real binary with stub adapters (local HTTP stubs
for whisper/llm/embeddings, PATH shims for yt-dlp/trafilatura), no live
services in tests.

## 14. Phases

Each phase ships a working system. Task cards per phase live in `cards/`.

1. MVP: schema, Note Store + git, auth, POST/GET/PATCH/DELETE/append, index
   filters, worker with the transcription pipeline only.
2. Link and YouTube extraction, LLM tagging, retag endpoint.
3. Embeddings, related endpoint, merge, dedup hardening.
4. Reminders/defer surfacing polish, trash GC, push cron, hardening.

## 15. Non-goals (explicit)

No push notifications, no multi-user, no conflict-free multi-writer merging
beyond 409, no WebDAV, no calendar integration, no automatic re-tagging, no
frontend anything. A future notifier may read the public API; the backend
itself stays passive.

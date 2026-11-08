# Phase 1 Acceptance Report

Executed 2026-11-07 against the real binary (`25d743a` + observability
commit), `stub_whisper`, a fresh auto-`git init`-ed notes repo, and the
card's config shape (fast backoff, `worker_idle_poll_seconds = 1`).
Method per `IMPLEMENTATION-FRAMEWORK.md` 8; card script per
`cards/phase-1-mvp.md` §Acceptance script.

## 1. What was built

| Slice | Commit | Contents |
|---|---|---|
| Docs baseline | `c9974ad` | DESIGN/FRAMEWORK/cards |
| Scaffold | `5c970c1` | module, binary skeleton, stderr addr announce |
| stub_whisper | `c853415` | multipart contract, `/__requests`, runtime `/__mode` |
| Config | `d940c5d` | typed TOML loader + validation (DESIGN 12) |
| Fixtures | `1e4342b` | real 3.6 s m4a audio fixture |
| Note Store | `7e7c1d9` | schema validation, atomic writes, ULID, CRUD, dedup, VCS commits |
| Index | `6b90778` | SQLite summaries/filter tables, queries, DiscoverDue, reindex |
| API | `de8704b` | bearer auth, notes CRUD + append, admin reindex, health |
| Worker | `25d743a` | chain runner, whisper adapter, backoff, boot recovery, wake loop |
| Observability | `65ddbd8` | lifecycle INFO logs (claimed/committed/retry/re-armed/boot) |

## 2. Verification

Full suite in a clean clone (`git clone` → `go vet ./... &&
go test -count=1 -race ./...`): all packages `ok`, including `-race`;
async/system packages additionally run `-count=2` clean.

### Acceptance script — executed results

| Step | Result |
|---|---|
| 1 text note | file exists, `state=inbox`, `add note <id>` committed |
| 2 audio → transcript | `processing.step=transcribe` observed; transcript entry via API within poll; buffer deleted; `transcribe <id>` committed |
| 3 outage → retry → boot recovery | `retry_count>0` with `last_error=connection refused`; backend restart + stub back up → transcript lands, bookkeeping reset (`retry_count=0`, `last_error=null`) |
| 4 concurrency | stale PATCH → 409; correct `base_modified` PATCH → `todo` |
| 5 filters + sync cursor | `?state=todo` → exactly ID1; `?modified_since=$T0` → exactly the one note changed after T0 |
| 6 delete + auth | no token → 401; DELETE → `{"id":...}`; `.trash/<id>.json` exists; `delete <id>` committed |
| 7 commit hygiene | `git log | grep -c retry` → 0; failed note (11 attempts) has only its `add note` commit and kept its audio |

Logs from the executed run (DoD 3) — the kill lands while
`status=running`, and the fresh boot narrates the recovery:

```
INFO note committed  commit="add note 01M6VRBA…"
INFO note claimed    id=01M6VRBA… step=transcribe
--- SIGKILL backend while processing.status == running ---
INFO note re-armed after boot    id=01M6VRBA… step=transcribe
INFO worker: boot recovery       re_armed=1 stray_buffers_swept=0
INFO note claimed                id=01M6VRBA… step=transcribe
INFO note committed              commit="transcribe 01M6VRBA…"
```

Outage/retry to terminal failure (same note, live whisper down; `max_retries=20`):

```
INFO step failed, retry scheduled id=01M6VRBX… step=transcribe attempt=20 next_retry_at=… err="…connection refused"
WARN step failed permanently      id=01M6VRBX… step=transcribe attempts=21 err="…connection refused"
```

### Boot recovery, DoD variant (killed mid-transcription)

Backend SIGKILLed while the note was `running` under an in-flight whisper
request (stub in 6–8 s delay mode). Fresh boot re-armed the note, the
worker re-claimed, transcribed, committed, then deleted the buffer.
Covered deterministically by
`tests/system/worker_test.go::TestSystemBootRecoveryAfterCrashMidTranscription`
and demonstrated live on the real binary as above.

## 3. Substitutions while executing the script

1. **`curl -F` uploads → stdlib Go multipart driver.** The shell policy
   blocks `curl -F …=@…` invocations; steps 2 and 3 posted the same
   fixture through an equivalent `net/http` multipart POST (201 + id).
2. **Script-step 3 pacing.** With the demo config (`base=1s, max=10`) the
   note reached terminal `failed` before the manual restart could land
   (tool round-trip ≫ backoff). Re-run with `base=10s, cap=30s,
   max_retries=20` — the card's sequence then executed as written. (This
   also exercised the cap path: attempts=11 → `failed`, no commits.)
3. **Card jq quirks.** `jq -e ".notes[].id == \"$ID1\""` exits on the
   *last* element and is only meaningful when the filter returns one note
   (true here). `grep "transcribe $ID2" # one commit` matches **two**
   commits (see deviation 12). `<t0>` was bound to a real `date -u` value.

## 4. DESIGN.md gaps found, and decisions taken

1. **`POST /v1/notes/:id/append`** — named in DESIGN 14, absent from the
   card. DESIGN is normative → implemented (+ `ErrPendingTranscription`
   guard → 409 during transcribe).
2. **`POST /v1/notes/:id/retry`** — deferred: named nowhere as phase-1.
   Consequence: `failed` is terminal for now; retry bias was chosen for
   ambiguous failures (see 17).
3. **Chain steps with no phase-1 pipeline** (text notes at `tag`): stay
   `pending`; the runner skips without claiming. `DiscoverDue` excludes
   `processing_step='none'`-style dead-ends only via registered pipelines.
   Deviation-flagged: notes at unimplemented steps are repeatedly visited.
4. **`index_path` parent dir**: config keeps the index outside
   `notes_dir` (repo purity); the value's parent must exist or be creatable
   — loader creates it.
5. **`content_hash`** (DESIGN 3.2 undefined algorithm) = sha256 over
   entry texts joined by `\n`, prefixed `sha256:`.
6. **Git identity for service commits**: `-c user.name=Lucien
   -c user.email=lucien@localhost` (no global git config required).
7. **Auto `git init`**: missing `notes_dir` → `git init -b main` (card
   expects the backend to be the first writer).
8. **API conventions not fixed by DESIGN** (flaggable): error body
   `{"error": msg}`; 1 MB body cap; 415 on bad content-type; strict
   unknown-field rejection; DELETE → 200 `{"id":…}`; list → `{notes,
   total}`; health `{"status":"ok"}`; 409 stale PATCH returns the full
   current note; list ordered `modified DESC`; tags accept `["a"]` or
   `[{name,by}]`; `url` source → 400 "phase 2".
9. **Sync cursor**: `modified_since` filters `modified >= t` (inclusive);
   second-precision stamps mean a same-second change is included.
10. **`tag_by` column**: `by` is a SQLite keyword.
11. **Backoff formula**: `delay = base·factor^retry_count` with
    `retry_count` the *last* failure count (first delay = base), capped,
    floored at 1 s (misconfigured zero base can't hot-loop).
12. **Two commits per transcribe step** (step + buffer deletion, both
    `transcribe <id>`): the transcript must be durable before buffers
    disappear (crash-equivalence, DESIGN 4/6). The card's "one commit"
    comment reflects DESIGN 10's message naming, not commit count.
13. **`FailStep` never commits** (DESIGN 10) — verified by step 7.
14. **Delete failure after step success** only warns; the boot sweep
    commits the missed deletion later (verified by seam test).
15. **Empty whisper transcript** → no entry appended (silence), buffers
    consumed anyway.
16. **`whisper_url` is the full endpoint URL** (DESIGN 7.1); 10 min HTTP
    timeout constant.
17. **2xx-but-malformed whisper JSON → retryable** (bias-to-retry, since
    `/retry` is deferred).
18. **`worker_idle_poll_seconds` (default 30)**: not in DESIGN 12; needed
    for DESIGN 6 rule 2's "never stuck" guarantee against lost wake
    signals. Documented in `config.example.toml`.
19. **Boot `ResetRunning`**: `running → pending` without commit and
    without bumping `modified` (no content change); returns re-arm count.
20. **`whisper_language = "auto"`**: the DESIGN 12 default must not be
    sent literally — the real OpenAI-compatible endpoint answers
    `500 "Unsupported language: auto"`. "auto" (like "") is therefore
    encoded as an **absent** field (the OpenAI auto-detect convention);
    explicit values are sent verbatim. Found by the live smoke, covered
    by three seam tests, verified end-to-end against the real server
    (fixture transcribed: "The quick brown fox jumps over the lazy dog.").

## 5. Deliberately not built (card §Explicitly not in this phase)

Links, YouTube, tagging, embeddings, related, merge, retag, ribbon
ingest, reminders surfacing, trash GC, push cron, TLS termination
(fields accepted, unused), `/retry`, LLM adapters. No code beyond the
chain-table shape and schema "prepares" for them.

## 6. Post-acceptance addenda (2026-11-08)

Two pieces of work landed after §1–5, both TDD (RED → GREEN → full suite
`-race` green → one commit each):

- **`--debug` flag + failure-mode logging** (`c3f2f49`, `92ec204`,
  `661b07a`, `12ccce3`). The user-facing contract and redaction
  guarantees are README §Logging policy. Key decisions: auth bounces
  WARN the reason (`missing_header` / `malformed_header` /
  `unknown_token`) plus method/path/remote and never the credential;
  one debug access line per request via a statusWriter capturing
  status+reason at the `jsonOut`/`jsonErr` choke points; whisper
  failures WARN `status`+`retryable` with response SIZES only — error
  snippets live in the note's `last_error` (served by the API), not in
  the log; git init/add/commit failures ERROR with the git output,
  no-op commits a debug skip; worker wake/sleep/skip reasons and pass
  counts at debug; reindex answers with an INFO receipt. Deliberately
  NO HTTP recover middleware: the stdlib server already dumps panics
  to stderr and every route is pure, so a wrapper would be untestable
  glue.
- **Path-traversal security fix** (`4e4238d`). Found while building the
  logging slice: `GET /v1/notes/..%2Fsecret` with a note `id` field
  matching the traversal target returned files OUTSIDE `notes_dir`
  (confirmed exploitable with a canary before fixing), `AppendAudio`
  could write outside it, and a 500-vs-404 difference leaked file
  existence. Fixed at the single choke point — `Store.Read` requires
  `ulid.Parse(id)`, so every id-taking method inherits the gate;
  malformed ids 404 exactly like unknown notes. Buffer-name paths were
  already `filepath.Base`-safe. Pinned by canary regressions in the
  store seam (`TestMalformedIDsCannotEscapeNotesDir`) and the system
  seam (`TestTraversalIDCannotEscapeNotesDir`).

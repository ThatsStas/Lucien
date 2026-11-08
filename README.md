# Lucien — notes backend

Self-hosted REST backend for GTD-style notes. Notes are self-contained JSON
files in a git repository; slow work (transcription, extraction, tagging,
embedding) runs in an in-process worker. The normative design lives in
[`docs/DESIGN.md`](docs/DESIGN.md); the working method in
[`docs/IMPLEMENTATION-FRAMEWORK.md`](docs/IMPLEMENTATION-FRAMEWORK.md);
per-phase work in [`docs/cards/`](docs/cards/).

## Status

Phase 1 (MVP) in progress: schema, Note Store + git, auth, notes CRUD, index
filters, worker with the transcription pipeline.

## Build & test

```sh
go build ./...
go vet ./...
go test -count=1 -race ./...          # full suite: unit + seam + system
```

The system tests boot the real binary and `stub_whisper` on ephemeral
ports; no live network is touched. The stub speaks the whisper multipart
contract, records requests (`GET /__requests`) and switches failure mode
at runtime (`POST /__mode {"mode":"ok|error500|error422|refuse|delay"}`).

## Configure

Copy `config.example.toml`, fill `clients[].token_sha256` (SHA-256 hex of the
bearer token you hand to each client) and point `notes_dir` at your notes git
repository. The backend commits every content mutation to that repository.
The index lives outside the repo (`index_path`) and is derived: delete it and
`POST /v1/admin/reindex` rebuilds it from the note files.

## Run

```sh
go build -o lucien ./cmd/lucien
./lucien -config config.local.toml
```

The backend logs to stderr (`addr=host:port` once listening) and runs the
async worker in-process: audio notes are transcribed via `whisper_url`
with geometric backoff and boot recovery — a note left `running` by a
crashed process is re-armed to `pending` at startup, and audio buffers
survive until their transcript is committed.

Add `-debug` for verbose mechanics: the resolved config dump, one access
line per HTTP request, auth decisions, whisper calls and worker
sleep/wake cycles. Default level already shows every state transition
(INFO) and everything broken (WARN/ERROR). The level rules and redaction
guarantees are in the Logging policy below.

```sh
./lucien -config config.local.toml -debug
```

## Logging policy

Everything goes to one stderr stream (`slog`, text format). Levels are
picked so the default view holds only what an operator must see;
`-debug` adds mechanics.

| Level | When |
| --- | --- |
| `ERROR` | Server-side failures that affect a request or operation: git init/add/commit failures, index or boot failures, corrupt note files, pipeline panics |
| `WARN` | Retryable failures (whisper transport/5xx with `status`+`retryable`, scheduled retries), auth rejections with reason (`missing_header` / `malformed_header` / `unknown_token`), terminal note failures, unreadable files skipped |
| `INFO` | State transitions and operations: claim, commit, re-arm, boot-recovery summary (`re_armed`, `stray_buffers_swept`), repo initialization, reindex receipt |
| `DEBUG` | Per-request / per-call mechanics: one access line per request (`method path status dur_ms remote reason`), auth-accepted client name, whisper request/response (sizes, never bodies), git invocations, worker sleep/wake/skip reasons, resolved config dump |

Client-caused 4xx stays silent at the default level — it surfaces only in
the debug access line. Never logged, at any level: bearer tokens (right
or wrong), `token_sha256` values, note/entry text, whisper response
bodies (only their size is logged; an error snippet lives in the note's
own `last_error` field, which the API serves), and audio bytes.

## Exercise (smoke)

```sh
T=<raw token>; B=http://127.0.0.1:8188
# text note
curl -s -X POST $B/v1/notes -H "Authorization: Bearer $T" \
  -H 'Content-Type: application/json' -d '{"text":"buy milk"}'
# audio note (multipart upload; transcribes asynchronously)
curl -s -X POST $B/v1/notes -H "Authorization: Bearer $T" \
  -F file=@tests/fixtures/sample.m4a
# watch the pipeline: processing.step/status, then a transcript entry
curl -s $B/v1/notes/<id> -H "Authorization: Bearer $T" | jq .processing
```

Against a stub instead of a real whisper, boot `go run ./tests/stubs/stub_whisper
--port 9001` and point `whisper_url` at it. The full scripted acceptance walk
(CRUD, concurrency, filters, outage retry, kill-mid-transcription boot
recovery, commit hygiene) is `docs/cards/phase-1-mvp.md` §Acceptance script;
its executed transcript with deviations is
[`docs/ACCEPTANCE-PHASE1.md`](docs/ACCEPTANCE-PHASE1.md).


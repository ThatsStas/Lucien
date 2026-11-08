# Task Card: Web 5: Audio upload and processing polling

Audience: the implementing model, one card per fresh session.
Read order:

1. This card, fully.
2. `../FRONTEND-DESIGN.md`, fully, sections 5 (phase-1 realities), 9
   (`usePollNote`), 10 (stub_whisper harness).
3. `../IMPLEMENTATION-FRAMEWORK.md`, fully.

Cards 1–4 are merged. The e2e harness already runs the backend with
`whisper_url` pointed at `stub_whisper -port 18191`.

## Goal

Upload an audio file, watch its badge move through queued → transcribing →
rest without reloading, and see an honest failure state when the stub
whisper is down. After this card the v1 feature set is complete.

## Build

1. **Upload control.** `upload-button` (ElButton) triggers a hidden
   `<input type="file" accept=".m4a,.mp3,.wav,.ogg,.flac">`, no ElUpload.
   Choosing a file calls `createAudio(file)`, shows an ElMessage with the new
   id, and the list poll surfaces the row with badge "queued"
   (`transcribe`+`pending`). Map client-side rejections to text: 413 →
   "file too large", 415 → "unsupported audio format", 0 → "server
   unreachable". Also disable upload and show why when `offline` is set.
   Commit: `web(upload): multipart create via file input`.
2. **`usePollNote`, unit-test-first.** Build to section 9 against a stubbed
   Api with a fake scheduler: ticks once per second; stops on settled, on
   timeout, and on unmount; never two overlapping requests (next tick starts
   after the previous resolves); timeout reports the last note plus
   `timedOut: true`.
   Commit: `web(detail): usePollNote tick loop`.
3. **Wire it into detail and list.** Detail view: if the fetched note is
   `step=transcribe` with status `pending` or `running`, start
   `usePollNote(id, { settled: note => note.processing.step !==
   'transcribe' || (note.processing.status === 'failed' &&
   note.processing.retry_count >= 30), timeoutMs: 120_000 })`, 30 is above
   the demo config's `max_retries`, so terminal failure stops it naturally.
   Each poll response replaces the whole note panel. When the transcript
   lands (a `kind: transcript` entry appears), say so once via ElMessage.
   List rows do not poll individually; their 3 s cursor poll already refreshes
   badges. Text notes must not start a poll, assert this in a unit spec.
   Commit: `web(detail): poll until transcription settles`.
4. **Honest failure and blocked-append states.** `failed` badge shows
   `last_error` verbatim (truncated, title attr full) and `retry_count`.
   While `transcribe` is pending/running the editor from card 4 is disabled
   with hint "transcription in flight", the backend answers 409 on append
   and the UI should not volunteer users to it. No retry button (no endpoint,
   section 5).
   Commit: `web(detail): failure surface and in-flight guard`.

## Explicitly out of scope

Microphone recording, playback of stored audio (the backend serves no
`/media` route yet), appending a second audio file to a note, retrying
failed steps, the `min_age`/`max_age`/`processing_status` filter controls
(`processing_status=failed` triage UI earns its place in a later release,
not here).

## Acceptance

From `web/`: `pnpm lint && pnpm type-check && pnpm test:unit && pnpm test:e2e`.

`upload.spec.ts`, drives modes through the stub's runtime switch
(`POST http://127.0.0.1:18191/__mode` with `{"mode":"…"}`):

1. Stub `ok`: upload `tests/fixtures/sample.m4a` via `upload-button` →
   "queued" badge appears; open the note → "transcribing" or (fast stub)
   already rest; within 30 s the transcript entry (`stub transcript`) is
   visible and the "transcript landed" message fired once; harness asserts
   via `curl` that `processing.step` advanced past `transcribe` and
   `retry_count == 0`.
2. Stub `error500`, then upload → badge never reaches rest; after ~10 s the
   detail shows `failed`-style bookkeeping (`retry_count > 0` and
   `last_error` naming the 500). Switch stub to `ok` → transcript lands
   within the next 90 s backoff window without any page reload (poll keeps
   running until settled, that is the regression this step guards).
3. `delay` mode: upload, open detail, press `e` → editor disabled with the
   in-flight hint; after the transcript lands the editor becomes usable.
4. Upload `not-audio.txt` → error message "unsupported audio format";
   upload with the backend stopped → "server unreachable", and the row never appears.

## Definition of done

Framework section 8 per commit. Card-specific: exactly one polling
implementation exists (`usePollNote`; grep for `setInterval` finds it only
there and in `useNoteList`); every poll has a stated settle rule and timeout
in the call site; the e2e run leaves no orphan stub process (harness
teardown verified by `pgrep stub_whisper` being empty after `pnpm test:e2e`).

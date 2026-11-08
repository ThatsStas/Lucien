# Web Frontend: Design

Version 1.0, 2026-11-08. Status: approved for implementation.

Audience: an implementing language model with a small context window, building
one card at a time from `cards/`. You are assumed to know what HTTP, JSON and
npm packages are; everything Vue-specific this project needs is defined here.
The backend is implemented in this repo (`internal/api`); its behavior is
normative in `DESIGN.md` and you do not need to read that file, section 5
here carries the full frontend-facing contract, verified against
`internal/api/server.go` at the current HEAD.

## 0. Scope

In scope: a browser web app in `web/`, create text notes, list them with
filters and search, view one, edit it (entry text, human tags, priority,
state) with the 409 conflict flow, delete to trash, upload audio files, watch
processing status settle, and a settings screen holding the server token.

Out of scope: microphone recording, link/URL ingest UI, trash browsing and
restore, offline support, PWA service workers, push notifications, multi-user
concerns, TLS and reverse-proxy setup (operator's job), mobile-first layout
(desktop browser first; Android later through the same build).

## 1. Architecture in one rule

Three layers, data flows one way.

- `src/api/` is the only code that speaks HTTP and the only place the
  Authorization header appears. It never imports Vue.
- `src/composables/` own state, timers and multi-step flows (list sync, note
  polling). They import the client, never the DOM.
- `src/views/` and `src/components/` render and dispatch user intent. They
  never call `fetch`, never touch timers.

If a change would make a view import `fetch` or a store import `document`,
the change is misplaced. Fix the placement, not the rule.

## 2. Decisions ledger

Recorded so nobody relitigates them mid-card. A card that needs to break one
escalates in its final report instead of silently deviating.

| Decision | Choice | Rejected | Why |
|---|---|---|---|
| Platform | Plain browser website, desktop Chrome/Firefox evergreen; Android later via browser | Electron, Tauri, PWA | One artifact, zero shells; the backend contract already assumes poll-only clients |
| Repo | `web/` subdir of this repo | Separate repo | Card and contract stay adjacent; separation happens at build/deploy, not at git |
| Separation | `web/dist/` is its own deployable; never embedded in the Go binary | go:embed | Independent deploys and rollbacks were a stated goal of splitting the halves |
| Stack | Vue 3 + TypeScript strict + `<script setup>` SFCs, Vite via `create-vue`, vue-router | React, Svelte, Options API | Corpus coverage for the implementing model; scaffold settles tooling so cards spend no slices on config archaeology |
| State | Hand-rolled typed API client + composables; Pinia for the settings store only | TanStack Query, Pinia-as-cache | The `modified_since` cursor merge is bespoke sync; generic caches fight it |
| UI kit | Element Plus for chrome; note list and keymap hand-rolled | Tailwind-only, fully custom | Styling outsourced; the parts where keyboard focus is the product stay ours so library focus traps cannot collide with vim keys |
| Package manager | pnpm | npm, yarn | Speed and disk; any is fine, pnpm is the one the commands assume |
| Search | Pass `q` through to the backend's LIKE match; UI tests assert substring behavior only | Client-side ranking, embeddings | The real search is phase 2 (FTS5); asserting more than substring would lock us to interim behavior |
| Theme | Light for v1; Element Plus dark class and one CSS-variable file reserved | Dark toggle in v1 | Nearly free later, noise now |
| Tests | vitest + @vue/test-utils (unit), Playwright (e2e), ESLint + Prettier from the scaffold | Cypress, Jest | Matches the scaffold; e2e runs against the real binary like the backend's system tests |

## 3. Serving contract

Three lines, all of which must stay true:

1. Dev: `pnpm dev` serves the app at `http://127.0.0.1:5173` with Vite
   proxying `/v1` → `http://127.0.0.1:8188`.
2. Localhost near-production: `pnpm build && pnpm preview` serves
   `web/dist/` at `http://127.0.0.1:4173` with the same proxy.
3. Deployment: static `web/dist/` served same-origin with the API mounted at
   `/v1/*`. A reverse proxy (operator's future buildout) only has to satisfy
   this line.

Consequences the code must honor: the frontend calls relative `/v1/*` paths
only; no absolute origin is hardcoded anywhere; no CORS is configured
anywhere, on either side. The settings screen's server-URL override exists
for debugging only and its help text says so (cross-origin requests will
fail without CORS, by design).

## 4. Repo layout

```
web/
  package.json  vite.config.ts  playwright.config.ts  tsconfig*.json
  src/
    main.ts  App.vue  router.ts
    api/
      client.ts        # fetch wrapper + one exported function per endpoint
      types.ts         # TS mirrors of the wire structs, section 5
    stores/
      settings.ts      # the only Pinia store
    composables/
      useNoteList.ts   # list state, cursor poll, filters
      usePollNote.ts   # poll one note until processing settles
    components/
      NoteList.vue  NoteRow.vue  ProcessingBadge.vue
      NewNoteDialog.vue  ConflictDialog.vue  TagEditor.vue
    views/
      ListView.vue  NoteView.vue  SettingsView.vue
    lib/
      keymap.ts        # global key handler, section 7
    assets/
      theme.css        # Element Plus CSS-variable overrides, section 11
  tests/
    unit/              # vitest: client.ts, composables, components
    e2e/               # Playwright specs, one file per card area
    e2e/server/        # globalSetup harness, section 10
    acceptance.sh      # card 6 end-to-end gate
```

Cards may add files; they may not restructure this tree.

## 5. HTTP contract

Verified against `internal/api/server.go` and `internal/notestore` at HEAD.
This table is normative for the frontend; if it disagrees with the running
binary, the binary wins and the deviation goes in the card's final report.

All routes require `Authorization: Bearer <token>` except `/v1/health`.
Errors answer `{ "error": "<message>" }` with the status codes below.
JSON bodies reject unknown fields, the client must send exactly the
documented keys.

| Method, path | Request | Response | Notable errors |
|---|---|---|---|
| `GET /v1/health` | none | `200 {"status":"ok"}` | unauthenticated |
| `POST /v1/notes` (JSON) | `{"text": "...", "tags"?: ["a"] or [{"name":"a","by":"human"}], "state"?, "priority"?, "remind_at"?, "defer_until"?}` | `201 {"id":"01…"}` | 400 invalid body, unknown field, or `url` kind (phase 2) |
| `POST /v1/notes` (multipart) | `file` (m4a, mp3, wav, ogg, flac) + text fields `tags`, `state`, `priority`, `remind_at`, `defer_until` | `201 {"id":"01…"}` | 400 missing file, 413 over `max_upload_mb`, 415 bad extension |
| `GET /v1/notes` | query: `state` (repeatable), `tag` (repeatable), `q`, `modified_since` (RFC3339), `limit`, `offset`, `processing_status` (pending/running/failed), `remind_due`, `defer_due`, `min_age`, `max_age` | `200 {"notes": Summary[], "total": int}` | 400 invalid enum or timestamp |
| `GET /v1/notes/:id` | none | `200 Note` (full, from file) | 400 invalid id, 404 |
| `POST /v1/notes/:id/append` | JSON `{"text":"..."}` or multipart `file` | `200 Note` | 400, 404, 409 (transcription in flight), 413, 415 |
| `PATCH /v1/notes/:id` | `{"base_modified": "<RFC3339>", …}`, required; optional: `state`, `tags` (replaces set), `priority`, `remind_at`, `defer_until` (null clears), `entry_edit: {"id": int, "text": str}` | `200 Note` | **409 → body is the full current Note**; 400 invalid body/entry id; 404 |
| `DELETE /v1/notes/:id` | none | `200 {"id":"01…"}` | 404 |

Wire types, mirrored verbatim into `src/api/types.ts`:

```ts
type State = 'inbox' | 'todo' | 'in_progress' | 'done' | 'someday' | 'archived'
type TagBy = 'agent' | 'human'
interface Tag { name: string; by: TagBy }
interface Source { kind: 'text' | 'audio' | 'link' | 'youtube' | 'ribbon'; url: string | null }
interface Entry { id: number; ts: string; kind: 'text' | 'transcript' | 'excerpt'; text: string }
interface Processing { step: 'transcribe' | 'extract' | 'tag' | 'embed' | 'none'
  status: 'pending' | 'running' | 'failed'; retry_count: number
  next_retry_at: string | null; last_error: string | null }
interface Note { id: string; created: string; modified: string; state: State
  state_changed_at: string; tags: Tag[]; priority: number; source: Source
  entries: Entry[]; processing: Processing; remind_at: string | null
  defer_until: string | null; dedup: { url_hash: string | null
  content_hash: string; resaved: string[] } }
interface Summary { id: string; state: State; tags: Tag[]; priority: number
  created: string; modified: string; source: Source
  processing: { step: Processing['step']; status: Processing['status'] }
  snippet: string }
interface ListResult { notes: Summary[]; total: number }
```

Timestamps are RFC3339 strings; keep them as strings in the client and pass
them back unchanged as `base_modified`, parsing to `Date` and reserializing
can shift sub-second digits and manufacture a conflict.

Phase-1 realities the UI must honor:

- A text note parks at `step=tag, status=pending` and stays there (the tag
  pipeline arrives in phase 2). That is its resting state: render it as
  "queued for tagging", never as a spinner or an error.
- An audio note's real motion is `step=transcribe`: pending → running →
  (transcript entry lands, step advances to tag) or failed with `retry_count`
  and `last_error` set.
- There is no retry endpoint. Failed steps surface `last_error` in the UI;
  recovery happens server-side (or via the operator). Do not invent a retry
  button.
- `POST /v1/notes` with `url` answers 400. The create UI offers text and
  audio only.

## 6. Settings and bootstrap

One localStorage key, `lucien.settings`, holding one JSON object:

```ts
interface Settings { token: string; serverUrl: string; pageSize: number; theme: 'light' }
```

`serverUrl` defaults to `""`, meaning relative `/v1`. The settings store
(Pinia, the only store) loads it synchronously before the app mounts, writes
on every change (no Save button state to get wrong), and is read by
`client.ts` through an injected getter so unit tests never touch
localStorage.

Bootstrap rules:

- First paint with `token === ""` routes to `/settings` and stays there.
- Any API answer of 401 shows one toast ("authentication failed, check the
  token") and routes to `/settings`. A queued second 401 does not stack
  toasts or re-route.
- The token is stored plaintext. Accepted risk for a single-user homelab tool
  on a personal machine; revisit the moment the app leaves that machine.
- `/settings` holds: token input (password type), server-URL override
  (labeled debug-only), and a Test connection button that calls
  `GET /v1/health` then `GET /v1/notes?limit=1` and shows ok / 401 / network
  error as inline text, not a toast.

## 7. Keyboard model

The keymap is a global `keydown` listener installed once in `App.vue`,
dispatching through a table. It is architecture, not polish: every action a
key performs must also exist as a click target.

| Key | Where | Action |
|---|---|---|
| `j` / `k` | list | move selection down / up (clamp at ends, never wrap) |
| `Enter` | list | open selected note → `/n/<id>` |
| `e` | list, detail | open the editor for the selected/note |
| `n` | list | open the new-note dialog |
| `d` | list | delete selected note (confirm dialog first) |
| `/` | list | focus the search input |
| `Esc` | anywhere | close top dialog; blur search input (second press clears it) |

The handler ignores the event unless `document.activeElement` is `body` (or a
non-focusable element): any `input`, `textarea`, `select` or contenteditable
target, and any open dialog, makes vim keys inert. Element Plus dialogs keep
their own Escape handling; the global handler never processes a key while a
dialog is open. After a dialog closes, focus returns to `body` so `j/k` work
again immediately.

## 8. UI kit rules

Element Plus is for chrome: dialogs, forms, selects, buttons, toasts, tag
chips. Hand-rolled: `NoteList`/`NoteRow` (selection model is ours),
`ProcessingBadge`, `keymap.ts`, the upload control (a plain
`<input type="file">` behind an `ElButton`, no `ElUpload`).

Whitelist, anything else needs the card to justify it in the final report:
`ElButton`, `ElInput`, `ElInputNumber`, `ElSelect`, `ElOption`, `ElDialog`,
`ElForm`, `ElFormItem`, `ElTag`, `ElMessage`, `ElRadioGroup`, `ElSkeleton`.

Discipline that keeps the future library swap cheap (and the tests honest):

- Library components are leaves. No app state or data logic inside them;
  props in, events out.
- e2e tests select only by `data-testid` or ARIA role, never by Element Plus
  class names, which are library internals and churn with its versions.
- Every interactive element gets a stable `data-testid`. Names are kebab-case
  and listed in the spec that introduces the element; the running list:
  `search-input`, `state-filter`, `note-list`, `note-row-<id>`,
  `new-note-button`, `new-note-text`, `new-note-submit`, `state-select`,
  `delete-button`, `confirm-delete`, `upload-button`, `processing-badge`,
  `edit-toggle`, `entry-select`, `entry-text`, `entry-save`, `tag-input`,
  `tag-add`, `conflict-dialog`, `conflict-take-server`, `conflict-use-mine`,
  `settings-token`, `settings-server-url`, `settings-test`,
  `settings-test-result`.

## 9. Data layer

`client.ts` exports exactly these functions; a card adds a function only when
its feature needs one:

```ts
interface ApiError { status: number; message: string }  // message from {"error":…} or HTTP text

createClient(getSettings: () => { token: string; serverUrl: string },
             fetchImpl?: typeof fetch): Api
type Api = {
  health(): Promise<{ status: string }>
  listNotes(p: ListParams): Promise<ListResult>
  getNote(id: string): Promise<Note>
  createText(r: { text: string; state?: State; priority?: number; tags?: string[] }): Promise<{ id: string }>
  createAudio(file: File, meta?: { state?: State; priority?: number; tags?: string[] }): Promise<{ id: string }>
  appendText(id: string, text: string): Promise<Note>
  patchNote(id: string, patch: Patch): Promise<PatchResult>
  deleteNote(id: string): Promise<{ id: string }>
}
type PatchResult = { kind: 'ok'; note: Note } | { kind: 'conflict'; note: Note }
// patchNote resolves on 409 with the server's current note; every other
// non-2xx rejects with ApiError. Transport failure rejects with ApiError
// status 0.
```

The injected `getSettings` and `fetchImpl` are the seams vitest drives: no
component test ever needs a network.

`useNoteList()` owns `{ rows, total, selected, filters, q, loading }` and the
poll loop:

- Filter or search change: full `listNotes` fetch, selection resets to 0,
  cursor resets.
- Every 3 s: `listNotes({ ...filters, modified_since: cursor })`; merge the
  returned rows into `rows` by id (replace existing, add new, then re-sort by
  `modified` desc), drop nothing. A row whose id matches the selected row
  keeps selection.
- Cursor after any successful fetch: max `modified` seen, or the old cursor.
  A failed poll sets `offline: true` and retries on the next tick; the list
  keeps showing last-known data with a small "offline" marker rather than
  blanking.
- Page size comes from settings (`limit`), no infinite scroll in v1; `total >
  limit` renders a "showing X of Y" line.

`usePollNote(id, opts)` polls `getNote(id)` once per second until
`opts.settled(note)` or `opts.timeoutMs` (default 120 000) elapses, then
stops and reports the last note. Callers define settled, card 5 uses:
`step !== 'transcribe' || (status === 'failed' && retry_count >= maxRetries)`.
No caller polls unconditionally forever; text notes rest at `tag/pending`
(section 5) and must not keep a timer alive.

## 10. Test infrastructure

Unit (vitest + @vue/test-utils, `tests/unit/`): the client functions against
an injected `fetch` stub (assert URL, method, headers, body shape, error and
409 mapping); composables against a stubbed `Api`; components mount with a
stubbed `Api` provided. No network, ever, in unit tests.

e2e (Playwright, `tests/e2e/`): real stack, mirroring the backend's system
tests, no mocks of the thing under test.

- `tests/e2e/server/global-setup.ts` spawns, and kills on teardown:
  - `stub_whisper -port 18191 -mode ok`
  - the backend built from source (`go build -o …/lucien ./cmd/lucien`) with
    a generated TOML: `bind=127.0.0.1, port=18188`, `notes_dir` and index in
    a temp dir, `whisper_url=http://127.0.0.1:18191`, fast backoff,
    `token_sha256` of the fixed e2e token `e2e-token-not-a-secret` (hashed
    in the harness, never stored in the repo).
- `playwright.config.ts`: `webServer` runs `pnpm preview --port 4173
  --strictPort` (build beforehand in one global command), `baseURL
  http://127.0.0.1:4173`, chromium only for v1. If 18188/18191/4173 are
  taken, the harness fails fast naming the port.
- Logged-in fixture: `page.addInitScript` pre-fills `lucien.settings` with
  the e2e token before navigation. The "fresh, unauthenticated" flow gets its
  own spec that skips the fixture.
- Assertions about storage truth (git commits, `.trash` files, note JSON)
  read the temp dir or `curl` the API directly, the UI does not grade its
  own homework.

Commands, all green on every commit: `pnpm lint`, `pnpm format`,
`pnpm type-check`, `pnpm test:unit`, `pnpm test:e2e`.

## 11. Style and theme

One `theme.css` holding Element Plus CSS-variable overrides (primary color,
border radius, font stack), the only file that restyles the library, so
taste changes never scatter through components. Light theme for v1; the dark
toggle later is a `dark` class on `<html>` plus the variables Element Plus
already ships. The admin-panel default look is accepted: v1 optimizes for
function, and this is a tool you use with the keyboard, not a marketing page.

## 12. Working method

The loop per slice, the seams, and the definition of done come from
`IMPLEMENTATION-FRAMEWORK.md`, read it fully before card 1. Frontend
substitutions to its rules:

- Seam = Playwright against `pnpm preview` + the real binary for behavior;
  vitest for client and composable units. "Test at the seam" means the e2e
  spec, not a component test, carries each user-visible behavior.
- No fixed ports beyond the e2e block (18188/18191/4173), and the harness
  refuses to start when they are occupied.
- The commit discipline and the no-new-architecture rule apply unchanged.
  Frontend code lives under `web/` only; a card that wants to touch Go stops
  and reports.

# Task Card: Web 3: Note detail, state transitions, delete

Audience: the implementing model, one card per fresh session.
Read order:

1. This card, fully.
2. `../FRONTEND-DESIGN.md`, fully, sections 5, 7, 8, 9.
3. `../IMPLEMENTATION-FRAMEWORK.md`, fully.

Cards 1–2 are merged: client, settings, list with `j/k///n` navigation.

## Goal

Deep-linkable note view at `/n/:id` showing the full document from the file
(via the API), with state transitions, delete-to-trash, and the processing
bookkeeping surfaced as the file holds it. After this card the
read-and-organize loop is complete; only writing content is missing.

## Build

1. **Route + fetch-once view, test-first.** `/n/:id` route; `NoteView` calls
   `getNote(id)` once on mount through an injected Api; renders header (id,
   state, source kind, created/modified), the full `Processing` block
   (step, status, and when nonzero `retry_count` plus `last_error` verbatim),
   then entries as a timeline: each entry's kind, timestamp, text in full
   (`whitespace-pre-wrap`; entries are notes, they may hold newlines).
   Unknown id → backend 404 → friendly "note not found" panel with a back
   link, not a stack trace. Unit specs drive the injected Api.
   Commit: `web(detail): note view from full document`.
2. **State transitions.** Header holds `state-select` (ElSelect, the six
   states). Change → `patchNote(id, { base_modified: note.modified, state })`
   → on `ok` replace local note with the returned one (never trust a local
   guess for `modified`); on `conflict` see card 4 for the dialog, for now
   route it to a placeholder that shows the server note's `modified` and a
   Reload button, and leave a `TODO(card4)` marker. `Enter` on a selected
   list row now navigates here; `Esc` in the header returns to the list.
   Commit: `web(detail): state transitions via optimistic base_modified`.
3. **Delete.** `delete-button` on the detail header, `d` on a selected list
   row. Both open an ElDialog confirm (`confirm-delete` button names the
   note id). Confirm → `deleteNote(id)` → list: remove row, ElMessage
   `deleted <id>`; detail: route to `/` first, then the message. A 404 (someone
   deleted it server-side) removes the row with an info toast instead of
   erroring.
   Commit: `web(detail): delete to trash with confirm`.
4. **ProcessingBadge finals.** Badge variants: text-note rest (`tag` +
   `pending` → "queued for tagging", muted), audio `transcribe` pending
   ("queued"), running ("transcribing"), failed ("failed: <last_error>",
   truncated with title attr full text). The phase-1 rest state must not
   render as a spinner: text notes sit there forever (section 5).
   Commit: `web(detail): honest processing badges`.

## Explicitly out of scope

Editing entry text/tags/priority and the 409 conflict dialog (card 4;
placeholder hooks here are expected and replaced there), audio upload (card
5), live polling of one note (card 5 installs `usePollNote`; this card's
detail view fetches once and Reload refreshes), trash or restore, retry of
failed steps, there is no retry endpoint, do not invent one.

## Acceptance

From `web/`: `pnpm lint && pnpm type-check && pnpm test:unit && pnpm test:e2e`.

`detail.spec.ts`:

1. Seed a note via `curl`; open `page.goto('/n/<id>')` directly (fresh
   navigation, not a click) → full text renders; state select shows `inbox`.
2. Select `todo` → within 1 s `curl GET /v1/notes/<id>` says `state=todo`,
   and the header select still reads `todo` (local note replaced from the
   response).
3. `curl`-PATCH the same note's state to `done`, click Reload → view shows
   `done`. (Proves single-fetch + reload reads file truth.)
4. From the list, select the row, press `Enter` → URL is `/n/<id>`; `Esc` →
   back to `/` with selection restored.
5. Detail: click `delete-button`, `confirm-delete` → routed to `/`, row gone,
   and the harness temp dir holds `.trash/<id>.json` (assert on the file, the
   UI does not grade its own homework).
6. `page.goto('/n/01BORUS')` (malformed) or an unknown ULID → "note not
   found" panel, no uncaught console errors.

## Definition of done

Framework section 8 per commit. Card-specific: state change always sends
`base_modified` equal to the last `modified` the client read; no
component on this card imports `fetch` or sets a `setInterval` (polling is
card 5's composable); `ProcessingBadge` has a unit spec pinning the five
variants including the muted phase-1 rest state.

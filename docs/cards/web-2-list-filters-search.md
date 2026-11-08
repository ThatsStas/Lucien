# Task Card: Web 2: Note list, filters, search, keyboard navigation

Audience: the implementing model, one card per fresh session.
Read order:

1. This card, fully.
2. `../FRONTEND-DESIGN.md`, fully, sections 7, 8, 9 are your build spec.
3. `../IMPLEMENTATION-FRAMEWORK.md`, fully.

Card 1 is merged: the client, settings store, and Playwright harness exist.
`Enter`/`e`/`d` keys from the section 7 table belong to cards 3 and 4; card 2
installs the keymap mechanism with `j`, `k`, `/` and `n` wired.

## Goal

A live note list you drive without the mouse: rows with state, tags, source
kind and snippet; state filter and substring search; a new-note dialog; and
a 3-second cursor poll that pulls in notes created out of band.

## Build

1. **`useNoteList`, unit-test-first.** Build to section 9 against a stubbed
   `Api`: filter change resets cursor and selection; the 3 s poll merges by
   id, replaces existing rows, keeps `modified` desc order and the selected
   id; failed poll sets `offline` without blanking rows; cursor is max
   `modified` seen; `limit` comes from settings and `total > limit` exposes a
   `showing X of Y` value. Timer control via an injectable `now`/scheduler so
   tests advance time without waiting.
   Commit: `web(list): useNoteList with cursor merge`.
2. **`NoteList`/`NoteRow`, hand-rolled.** Table-less rows (`div` list with
   `role="list"`), `data-testid="note-row-<id>"`, `aria-current` on the
   selected row, columns: state, tags (ElTag chips), source kind icon or
   text, snippet (200 chars come from the server), modified relative time,
   `ProcessingBadge` (text notes: "queued for tagging"; audio: step+status
   per section 5 phase-1 realities). Selection is an index in the composable;
   clicking a row selects it.
   Commit: `web(list): row rendering and selection`.
3. **Keymap.** `src/lib/keymap.ts` per section 7: one global listener, the
   inert-guard rules verbatim (input/textarea/select/contenteditable target,
   open dialog), dispatch table. Card 2 wires `j`, `k`, `/`, `n`. Unit tests
   cover the guard matrix (key ignored when typing, honored when body has
   focus), this file is reused by every later card, so its tests are strict.
   Commit: `web(keys): global keymap with input guards`.
4. **Filters + search + new-note dialog.** `search-input` (debounced 300 ms,
   feeds `q`), `state-filter` ElSelect (All + the six states, feeds `state`),
   `new-note-button` opens `NewNoteDialog` (ElDialog, ElInput textarea
   `new-note-text`, submit `new-note-submit`) calling `createText`; on
   success: close, ElMessage, the poll or an immediate refetch surfaces the
   row. `n` opens the dialog; `Esc` closes it and returns focus to body.
   Commit: `web(list): filters, search, quick create`.

## Explicitly out of scope

`Enter`/`e`/`d` actions (cards 3–4), note detail, editing, upload, sorting
controls, pagination beyond the limit line, tag filter UI (the client
function already supports `tag`; no control ships yet).

## Acceptance

From `web/`: `pnpm lint && pnpm type-check && pnpm test:unit && pnpm test:e2e`.

`list.spec.ts` (fixture token; seed with `curl -s -H "Authorization: Bearer …"
-d '{"text":"milkshake recipe"}' http://127.0.0.1:18188/v1/notes` style
POSTs, three notes with distinct text):

1. Page shows the three rows, `note-row-<id>` present, `showing 3 of 3`.
2. Press `/`, type `milk` → only the milkshake row remains, in ≤1 s after
   typing stops. Clear it.
3. `state-filter` → `todo`: list empties (all seeds are inbox); back to All.
4. `j` twice moves `aria-current` to the third row, `k` back to second; at
   the first row `k` stays; at the last `j` stays. Typing `jjjk` into
   `search-input` types letters, moves nothing.
5. `n`, type `keyboard created`, submit → dialog closes, toast shows, new row
   appears ≤4 s.
6. Out of band: `curl` a fourth note → appears ≤5 s with no reload.

## Definition of done

Framework section 8 per commit. Card-specific: `keymap.ts` has zero imports
from Element Plus (it is library-free by design); the poll is one interval,
verifiable by a unit test asserting `listNotes` call counts as the fake
scheduler advances; search assertions stay substring-only, a spec that
asserts result *order* beyond the server's `modified` desc order is wrong
(interim LIKE search, section 2).

# Task Card: Web 4: Editing and the 409 conflict flow

Audience: the implementing model, one card per fresh session.
Read order:

1. This card, fully.
2. `../FRONTEND-DESIGN.md`, fully, sections 5, 7, 8, 9, especially
   `patchNote`'s conflict resolution and the timestamp-strings rule.
3. `../IMPLEMENTATION-FRAMEWORK.md`, fully.

Cards 1–3 are merged: detail view with the conflict placeholder and
`TODO(card4)` marker, which this card replaces.

## Goal

Edit a note's entry text, human tags and priority; when the file moved under
you, meet the server's version and choose deliberately instead of silently
overwriting. The 409 flow is the point of this card: a lost keystroke is
annoying, a lost *note* is a bug report.

## Build

1. **Edit mode on detail, test-first.** `edit-toggle` button and `e` key
   (list and detail, per the keymap table) open the editor: `entry-select`
   (ElSelect of existing entries by id+kind, edit one entry per save),
   `entry-text` (ElInput textarea), priority (`ElInputNumber`, 0–3), and
   `TagEditor`. Save → `patchNote` with `entry_edit`, `priority` and `tags`
   in one call, `base_modified` = the note's `modified` you last read.
   On `ok`: replace local note, exit edit mode, ElMessage `saved`.
   Commit: `web(edit): entry, priority, tag editing`.
2. **TagEditor.** Renders chips from `note.tags`: `by: human` chips are
   removable, `by: agent` chips render muted with a lock affordance and no
   remove button (the agent owns them; phase 2 will overwrite them wholesale).
   `tag-input` + `tag-add` append human tags; duplicate names are rejected
   client-side with an inline hint (the backend accepts duplicates and you
   will not ship that as a feature). Save sends the full tag array in the
   `tags` patch field, replacement, not merge.
   Commit: `web(edit): human tags editable, agent tags read-only`.
3. **ConflictDialog, test-first.** Replace card 3's placeholder:
   `patchNote` resolving `{kind:'conflict', note}` opens `conflict-dialog`
   side-by-side: left = server note as returned by the 409 (its `modified`,
   the entries at that id, tags, priority); right = what you were about to
   send. Two buttons:
   - `conflict-take-server`: discard mine, adopt the server note as local
     truth, stay in edit mode with its values in the form.
   - `conflict-use-mine`: resend the same patch with `base_modified` = the
     **server's** `modified` (the one the dialog shows). The backend is
     last-writer-wins *after the loser admits it saw the winner*, that is
     the whole mechanism, do not send the stale base again.
     If the resend conflicts again (a third writer), the dialog reopens with
     fresh server data; no retry loop in code.
   Unit specs with a scripted Api drive both paths and the reopen.
   Commit: `web(edit): 409 merge dialog`.

## Explicitly out of scope

`remind_at`/`defer_until` editing (backend supports them; no UI pressure in
v1), appending entries (`appendText` exists in the client, unused, delete-or-
use decision belongs to card 6's refactor, default: keep, it's tested),
merging texts line-by-line (the dialog offers whole-note choices only),
conflict UI for state changes made through `state-select` (they ride the same
`patchNote` and therefore get the same dialog for free, verify, don't rebuild).

## Acceptance

From `web/`: `pnpm lint && pnpm type-check && pnpm test:unit && pnpm test:e2e`.

`edit.spec.ts`:

1. Seed a note; press `e` on its list row → detail opens in edit mode.
   Change entry text to `v2 text`, save → `curl` shows `entries[0].text ==
   v2 text`; toast `saved`.
2. Tags: add `groceries` (human), save → `curl` shows `{name:"groceries",
   by:"human"}`. Seed another note via `curl` with `{"tags":[{"name":"agent-x",
   "by":"agent"}]}` → its chip has no remove button.
3. Conflict, take server: open a note in the UI; `curl` PATCH its entry text
   to `server wins` with a fresh `base_modified`; UI edits the same entry and
   saves → `conflict-dialog` shows `server wins` on the left and my draft on
   the right; `conflict-take-server` → form shows `server wins`, and the
   still-stale save attempt did **not** touch the file (`curl` unchanged).
4. Conflict, use mine: repeat the race, choose `conflict-use-mine` → save
   succeeds, `curl` shows my text, and the note's `modified` advanced.
5. `state-select` during an out-of-band change also raises the dialog
   (same code path), closes cleanly on take-server.

## Definition of done

Framework section 8 per commit. Card-specific: exactly one code path sends
PATCH (unit spec asserts `patchNote` is the only importer-visible writer);
after `conflict-use-mine` succeeds, local `modified` equals the response's;
no UI anywhere can produce `by:"agent"` tags; `grep -rn "base_modified"
web/src` shows it only in `client.ts` and where a caller forwards a note
field, never a computed or defaulted timestamp.

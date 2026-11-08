# Task Card: Web 6: Refactor pass, full acceptance, DoD report

Audience: the implementing model, one card per fresh session.
Read order:

1. This card, fully.
2. `../FRONTEND-DESIGN.md`, fully.
3. `../IMPLEMENTATION-FRAMEWORK.md`, fully, especially sections on
   refactoring and the definition of done.

Cards 1–5 are merged: the v1 feature set exists and its suites are green.

## Goal

Bring the frontend to the standard the backend holds itself to: duplication
factored at the second caller, the keymap bulletproof at the edges, tests
green under refactor commits, one acceptance script proving the whole stack,
and the DoD report that tells the next phase what was bent.

## Build

1. **Refactor pass, suite-green throughout.** Each refactor is its own
   commit, every commit ends with all five gates green (`lint`, `format`,
   `type-check`, `test:unit`, `test:e2e`). Known debt to clear:
   - Factoring: shared relative-time formatter (list + detail, second
     caller has appeared, factor it into `src/lib/`); shared error-to-text
     mapping (upload messages from card 5 and the 401 toast from card 1 are
     the same table).
   - `client.ts` exports: every function has a caller or a unit spec justifying
     it; `appendText` stays as the endpoint-map mirror with its spec (record
     this in the report, deletion was considered).
   - Keymap edge audit, driven by new unit specs: keys inert inside dialogs,
     inside any input-like target, and while an IME composition is active
     (`keyup` vs `keydown`, `e.isComposing`); focus returns to `body` after
     each dialog close; no dead keys (every table entry has a handler and a
     test).
   - A11y: list rows are `role="listitem"` with `aria-current`; every
     control reachable and labeled by keyboard alone (run the specs with
     `page.keyboard` only, no `page.click` in the keyboard-path spec);
     dialogs trap focus via their library default; `ProcessingBadge` text is
     not color-only.
   - `pnpm lint`: zero warnings, zero errors, not the usual zero-error bar.
2. **Acceptance script.** `web/tests/acceptance.sh`, the sibling of the
   backend's: fresh temp notes dir, binary built from source, stub_whisper,
   generated config, then the numbered flow with `curl` truth checks printed
   as `PASS <step>` lines and Playwright (`pnpm test:e2e`) as the UI leg:
   1 text note created through the UI, `curl` shows the file and its git
   commit; 2 audio note uploads, transcript lands, buffer deleted; 3 stub
   outage → `retry_count` climbs → stub restored → transcript lands; 4
   conflict race resolved `take-server` and `use-mine`, file truth asserted
   after each; 5 state transition + delete to `.trash`; 6 token removed →
   settings screen; 401 → settings screen. Script exits non-zero on the first
   failed step and leaves its temp dir path in the output for inspection.
3. **DoD report.** `docs/ACCEPTANCE-WEB-1.md` in the shape of
   `ACCEPTANCE-PHASE1.md`: slice/commit table, executed results per script
   step, substitutions made while executing, deviations found between
   FRONTEND-DESIGN.md section 5 and the running binary, and the list of
   deferred decisions (processing_status filter UI, remind/defer editing,
   dark toggle, playback) with one line each on why they stayed out.
4. **README.** Root `README.md`: a Web section, where `web/` lives, the
   three commands (`dev`, `build && preview`, `test:e2e`), the serving
   contract in three lines, pointer to `cards/web-*.md`.

## Explicitly out of scope

New features. A gap found here is reported, not built, except the specs
this card names. No Go files change; if acceptance seems to need one, stop
and report.

## Acceptance

```bash
cd web && pnpm lint && pnpm format && pnpm type-check \
  && pnpm test:unit && pnpm test:e2e
bash web/tests/acceptance.sh   # PASS lines for steps 1–6, exit 0
```

`docs/ACCEPTANCE-WEB-1.md` exists, its commit table matches `git log` for the
web commits, and every step row carries an executed result, no "should".

## Definition of done

Framework section 8 in full, plus: `grep -rn "setInterval\|setTimeout"
web/src` hits only `useNoteList`, `usePollNote`, the health footer, and the
debounce; no component imports Element Plus components outside `<template>`
plus their required imports (no imperative service calls beyond `ElMessage`,
which is whitelisted once in a helper both toasts use); the acceptance script
runs green twice in a row on a clean checkout (flakes found here are fixed
here, never deferred, a flaky suite is the thing that makes phase 2 slow).

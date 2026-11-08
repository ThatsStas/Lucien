# Lucien web UI

The web frontend for the Lucien notes backend (Vue 3 + TypeScript). The
normative design is [`docs/FRONTEND-DESIGN.md`](../docs/FRONTEND-DESIGN.md)
(HTTP contract, decisions ledger, three-layer rule, keymap, Element Plus
containment, test infrastructure); the build is driven by
[`docs/cards/web-1…web-6`](../docs/cards/), one card per face.

Stack: Vue 3, vue-router, Pinia, Element Plus, Vite. Unit tests: Vitest.
End-to-end: Playwright against the REAL backend binary and `stub_whisper`
— no mocks. Package manager: pnpm.

## Run it

```sh
pnpm install
pnpm dev            # http://127.0.0.1:5173
```

Both `dev` and `preview` proxy `/v1` to the backend at
`http://127.0.0.1:8188` (FRONTEND-DESIGN.md 3), so start the backend
first (`../README.md#run`). No app code may reference an origin — the
proxy is the only place the backend lives. On first paint the bootstrap
guard routes to Settings until a server URL + token are saved; settings
(token, server URL, page size, theme) persist in a single localStorage
key and survive a corrupt store without crashing first paint.

## Commands

| Command | What it does |
| --- | --- |
| `pnpm dev` | Vite dev server with `/v1` proxy |
| `pnpm build` | `type-check` + production build into `dist/` |
| `pnpm test:unit` | Vitest unit/component suite (jsdom) |
| `pnpm test:e2e` | Playwright suite (see below) |
| `pnpm lint` / `pnpm format` | oxlint + eslint / prettier |

## End-to-end tests

`pnpm test:e2e` runs the full stack for real
(`tests/e2e/server/global-setup.ts`): it builds the Go backend and
`stub_whisper` from source, boots them on fixed ports — backend `18188`,
stub whisper `18191` — serves the built frontend on preview port `4173`,
and tears everything down afterwards. The e2e token is hashed inside the
harness and never committed. Because the suite is single-worker and owns
those ports, stop anything holding `18188`/`18191`/`4173` first (e.g. a
manually started backend on 8188 is fine; those three must be free).

## Status

Cards `web-1` (scaffold, settings, typed API client), `web-2` (list:
filters, search, quick create, keymap), `web-3` (detail: view, state
transitions, delete-to-trash, processing badges) and `web-4` (editing:
entry text, human tags, priority, 409 merge dialog) are done, each
committed green with unit and e2e coverage. `web-5` (audio upload with
transcription polling) and `web-6` (refactor + acceptance) are pending.

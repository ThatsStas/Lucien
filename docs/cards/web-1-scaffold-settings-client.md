# Task Card: Web 1: Scaffold, settings, API client

Audience: the implementing model, one card per fresh session.
Read order:

1. This card, fully.
2. `../FRONTEND-DESIGN.md`, fully.
3. `../IMPLEMENTATION-FRAMEWORK.md`, fully.

You do not need `DESIGN.md`; the HTTP contract you build against is
FRONTEND-DESIGN.md section 5.

## Goal

A Vue app that boots, redirects to a settings screen without a token, stores
the token, reaches the live backend, and ships a typed API client proven by
unit tests against an injected fetch stub. After this card every later card
adds features through `client.ts` and composables, nobody adds a second HTTP
path.

## Build

1. **Scaffold.** In the repo root:
   `pnpm create vue@latest web -- --typescript --router --pinia --test-vitest --e2e-playwright --eslint-with-prettier`
   (if the CLI prompts instead of accepting flags, pick exactly those
   features). `pnpm install`. Keep the scaffold's ESLint/prettier config as
   generated. Configure `vite.config.ts`: `server.proxy` and `preview.proxy`
   both `'/v1': 'http://127.0.0.1:8188'`, one shared `proxy` const, section 3.
   Commit: `web(scaffold): create-vue app, vite proxy to backend`.
2. **Types + client, test-first.** Write `src/api/types.ts` as the exact
   mirror of FRONTEND-DESIGN.md section 5. Then, one test at a time against
   an injected `fetch` stub, build `client.ts` to the section 9 signatures:
   tests assert the URL (relative `/v1/...`, or the settings serverUrl), the
   `Authorization: Bearer` header, JSON body containing only the documented
   keys, multipart construction (`FormData` with `file` plus scalar fields),
   `{error}` message extraction, transport failure → `ApiError` status 0,
   and `patchNote` resolving (not rejecting) on 409 with the server note.
   Commit: `web(api): typed client + settings-injected transport`.
3. **Settings store + view, test-first.** Pinia store per section 6: loads
   `lucien.settings` before mount, writes on change, defaults correct.
   `SettingsView` with token, server-URL (labeled debug-only), Test
   connection calling `health()` then `listNotes({limit:1})`, result as
   inline text (`settings-test-result`). Router guard: empty token →
   `/settings`; any client `ApiError` status 401 → one toast + route there.
   Unit-test the store against a localStorage stub; the 401 rule is tested by
   feeding a stubbed Api rejection.
   Commit: `web(settings): persisted token store, bootstrap guard, test connection`.
4. **Playwright harness.** Build `tests/e2e/server/global-setup.ts` exactly
   per section 10 (build backend binary, temp notes dir, generated TOML,
   stub_whisper on 18191, port preflight), `playwright.config.ts` per section
   10, and the logged-in fixture via `addInitScript`.
   Commit: `web(e2e): playwright harness against real binary and stub_whisper`.
5. **Health view.** `App.vue` shows router-view plus a footer line reading
   `GET /v1/health` state ("backend ok" / "backend unreachable"), refreshed
   every 10 s via the client. This gives card 2 an unauthenticated-proof
   target and proves the proxy in preview.
   Commit: `web(app): health footer, router shell`.

## Explicitly out of scope

Note list, detail, editor, upload, badges, keymap beyond nothing (card 2
installs it), dark mode, the server-URL override working
cross-origin.

## Acceptance

Run from `web/`:

```bash
pnpm lint && pnpm format && pnpm type-check
pnpm test:unit        # green: client + settings-store specs
pnpm test:e2e         # green: settings.spec.ts + health.spec.ts
```

`settings.spec.ts` (fresh context, no fixture): first visit lands on
`/settings`; type token into `settings-token`, press `settings-test`, see
`settings-test-result` containing `ok`; save; reload; the token is still
loaded and the app renders the shell. Wrong token → test result shows the
401 text; forcing a later API call with the bad token routes back to
`/settings` with one toast.

`health.spec.ts`: with the fixture token, footer shows `backend ok`. Stop
the backend in the spec (kill the harness process), footer flips to
unreachable within 15 s, restart, flips back.

## Definition of done

`IMPLEMENTATION-FRAMEWORK.md` section 8, applied per commit. Card-specific:
`grep -rn "fetch(" web/src --include=*.vue` is empty (only `client.ts`
touches fetch); no hardcoded `127.0.0.1` or `localhost` outside
`vite.config.ts`, the e2e harness, and the settings serverUrl help text;
`lucien.settings` is the only localStorage key the app reads or writes.
Final report lists any place where the running binary disagrees with the
section 5 table.

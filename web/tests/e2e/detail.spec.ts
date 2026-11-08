// Note detail acceptance (card web-3): deep link, state transitions with
// server-truth reload, Enter/Esc round trip with selection restored, delete
// to trash, and the malformed-id panel. Runs first alphabetically, so it
// must hand the harness backend back empty: every seeded id is deleted in
// afterAll, which list.spec's exact counts depend on.

import fs from 'node:fs'
import path from 'node:path'

import { expect, test } from './fixture'

import { BACKEND_PORT, E2E_TOKEN } from './server/global-setup'

const BASE = `http://127.0.0.1:${BACKEND_PORT}`
const seeded: string[] = []

async function api(pathname: string, init?: RequestInit): Promise<Response> {
  return fetch(`${BASE}${pathname}`, {
    ...init,
    headers: { Authorization: `Bearer ${E2E_TOKEN}`, ...init?.headers },
  })
}

// Sequential on purpose: the notes dir is a git repo and the backend
// commits per write; concurrent creates race on .git/index.lock (card 2
// deviation report).
async function seed(text: string): Promise<string> {
  const res = await api('/v1/notes', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ text }),
  })
  if (!res.ok) throw new Error(`seeding "${text}" failed: HTTP ${res.status}`)
  const body = (await res.json()) as { id: string }
  seeded.push(body.id)
  return body.id
}

async function getNote(id: string): Promise<Record<string, unknown>> {
  const res = await api(`/v1/notes/${id}`)
  if (!res.ok) throw new Error(`GET ${id} failed: HTTP ${res.status}`)
  return (await res.json()) as Record<string, unknown>
}

function rows(page: import('@playwright/test').Page) {
  return page.locator('[data-testid^="note-row-"]')
}

test.describe('note detail', () => {
  let noteA = ''

  test.afterAll(async () => {
    for (const id of seeded) {
      await api(`/v1/notes/${id}`, { method: 'DELETE' }) // tolerate 404
    }
  })

  test('deep link renders the full document and the inbox state', async ({ page }) => {
    noteA = await seed('groceries list\nline two is longer')
    await page.goto(`/n/${noteA}`)
    await expect(page.getByTestId('note-id')).toHaveText(noteA)
    // Newlines survive to the DOM: textContent, not a whitespace-normalizing
    // matcher, is the honest check for whitespace-pre-wrap.
    const pre = page.locator('[data-testid="entry-1"] pre')
    expect(await pre.textContent()).toBe('groceries list\nline two is longer')
    await expect(page.getByTestId('state-select')).toContainText('inbox')
  })

  test('choosing todo patches with base_modified; server and header agree', async ({
    page,
  }) => {
    await page.goto(`/n/${noteA}`)
    await page.getByTestId('state-select').click()
    await page.getByRole('option', { name: 'todo', exact: true }).click()
    // Card: within 1 s the file (via the API) says todo.
    await expect
      .poll(async () => (await getNote(noteA)).state, { timeout: 1_000 })
      .toBe('todo')
    // Local note was replaced from the PATCH response, not a local guess.
    await expect(page.getByTestId('state-select')).toContainText('todo')
  })

  test('out-of-band change is invisible until Reload reads file truth', async ({
    page,
  }) => {
    await page.goto(`/n/${noteA}`)
    await expect(page.getByTestId('state-select')).toContainText('todo')

    const current = await getNote(noteA)
    const res = await api(`/v1/notes/${noteA}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ base_modified: current.modified, state: 'done' }),
    })
    if (!res.ok) throw new Error(`out-of-band PATCH failed: HTTP ${res.status}`)

    // Fetch-once view: no background poll means the stale value stays put.
    await expect(page.getByTestId('state-select')).toContainText('todo')
    await page.getByTestId('reload-button').click()
    await expect(page.getByTestId('state-select')).toContainText('done')
  })

  test('Enter opens the selected row and Esc returns with selection kept', async ({
    page,
  }) => {
    await seed('second detail note')
    await page.goto('/')
    await expect(rows(page)).toHaveCount(2)

    await page.keyboard.press('j') // select the second row
    const selected = rows(page).nth(1)
    await expect(selected).toHaveAttribute('aria-current', 'true')
    const rowId = (await selected.getAttribute('data-testid'))!.replace('note-row-', '')

    await page.keyboard.press('Enter')
    await expect(page).toHaveURL(new RegExp(`/n/${rowId}$`))

    await page.keyboard.press('Escape')
    await expect(page).toHaveURL(/\/$/)
    await expect(rows(page).nth(1)).toHaveAttribute('aria-current', 'true')
  })

  test('delete from detail routes home, drops the row, files hit .trash', async ({
    page,
  }) => {
    const doomed = await seed('note destined for the trash')
    await page.goto(`/n/${doomed}`)
    await page.getByTestId('delete-button').click()
    const confirm = page.getByTestId('confirm-delete')
    await expect(confirm).toContainText(doomed)
    await confirm.click()

    await expect(page).toHaveURL(/\/$/)
    await expect(page.getByTestId(`note-row-${doomed}`)).toHaveCount(0)

    // The harness owns the truth: the file moved to the trash directory.
    const tmp = process.env.LUCIEN_E2E_TMP_DIR
    if (!tmp) throw new Error('LUCIEN_E2E_TMP_DIR not exported by the harness')
    const trashed = path.join(tmp, 'notes', '.trash', `${doomed}.json`)
    await expect.poll(() => fs.existsSync(trashed), { timeout: 2_000 }).toBe(true)
  })

  test('a malformed id shows the not-found panel without uncaught errors', async ({
    page,
  }) => {
    const uncaught: string[] = []
    page.on('pageerror', (err) => uncaught.push(err.message))
    // Chromium logs expected 4xx fetches as console errors; those are the
    // API doing its job, not the app crashing.
    page.on('console', (msg) => {
      if (msg.type() === 'error' && !msg.text().includes('Failed to load resource')) {
        uncaught.push(msg.text())
      }
    })
    await page.goto('/n/01BORUS')
    await expect(page.getByTestId('not-found')).toContainText('note not found')
    expect(uncaught).toEqual([])
  })
})

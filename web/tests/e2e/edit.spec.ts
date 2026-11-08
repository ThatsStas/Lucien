// Editing acceptance (card web-4): the e key into the editor, one-save
// patches for entry/priority/tags, human tag editing with agent tags locked,
// and the 409 dialog — take-server and use-mine — driven by real out-of-band
// PATCHes through curl-equivalent fetch. Like detail.spec, this file runs
// against the shared harness backend and must leave it empty: afterAll
// deletes every id it seeded, which list.spec's exact counts depend on.

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

// Sequential on purpose: concurrent creates race on .git/index.lock.
async function seed(text: string, extra: Record<string, unknown> = {}): Promise<string> {
  const res = await api('/v1/notes', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ text, ...extra }),
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

// Out-of-band mutation with the file's current modified: the exact race the
// 409 exists for. Two backend facts make this delicate: modified is
// truncated to whole seconds (Store.Now) and no-op writes never advance it
// (contentEqual). A race write landing inside uiBase's second is therefore
// invisible to the staleness check and the UI's save would wrongly succeed.
// Force the race write into a strictly later second, then verify it landed.
async function racePatch(id: string, uiBase: string, patch: Record<string, unknown>): Promise<void> {
  const wait = Date.parse(uiBase) + 1_100 - Date.now()
  if (wait > 0) await new Promise((r) => setTimeout(r, wait))
  const current = await getNote(id)
  const res = await api(`/v1/notes/${id}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ base_modified: current.modified, ...patch }),
  })
  if (!res.ok) throw new Error(`out-of-band PATCH failed: HTTP ${res.status}`)
  const after = await getNote(id)
  if ((after as { modified: string }).modified === uiBase)
    throw new Error('race write left modified unchanged: same-second edits are invisible to the staleness check')
}

function entryText(note: Record<string, unknown>): string {
  const entries = (note as { entries: { text: string }[] }).entries
  return entries[0]!.text
}

test.describe('editing and conflict resolution', () => {
  let noteA = ''

  test.afterAll(async () => {
    for (const id of seeded) {
      await api(`/v1/notes/${id}`, { method: 'DELETE' }) // tolerate 404
    }
  })

  test('e on a list row opens the editor; save writes the new text', async ({ page }) => {
    noteA = await seed('editable note')
    await page.goto('/')
    await expect(page.locator('[data-testid^="note-row-"]')).toHaveCount(1)

    await page.keyboard.press('Escape') // blur anything focused
    await page.keyboard.press('e')
    await expect(page).toHaveURL(new RegExp(`/n/${noteA}\\?edit=1$`))
    await expect(page.getByTestId('edit-form')).toBeVisible()

    await page.getByTestId('entry-text').fill('v2 text')
    await page.getByTestId('entry-save').click()
    await expect(page.getByText('saved', { exact: true })).toBeVisible()
    await expect
      .poll(async () => entryText(await getNote(noteA)), { timeout: 2_000 })
      .toBe('v2 text')
  })

  test('a human tag is added through the editor; agent chips carry no remove button', async ({
    page,
  }) => {
    await page.goto(`/n/${noteA}?edit=1`)
    await page.getByTestId('tag-input').fill('groceries')
    await page.getByTestId('tag-add').click()
    await expect(page.getByTestId('tag-chip-groceries')).toBeVisible()
    await page.getByTestId('entry-save').click()
    await expect(page.getByText('saved', { exact: true })).toBeVisible()
    await expect
      .poll(
        async () =>
          ((await getNote(noteA)) as { tags: { name: string; by: string }[] }).tags,
        { timeout: 2_000 },
      )
      .toEqual(expect.arrayContaining([{ name: 'groceries', by: 'human' }]))

    const agentNote = await seed('agent tagged note', {
      tags: [{ name: 'agent-x', by: 'agent' }],
    })
    await page.goto(`/n/${agentNote}?edit=1`)
    await expect(page.getByTestId('tag-chip-agent-x')).toBeVisible()
    // The agent owns the tag: no remove button anywhere in that chip.
    await expect(page.getByTestId('tag-remove-agent-x')).toHaveCount(0)
    await expect(page.getByTestId('tag-lock-agent-x')).toBeVisible()
  })

  test('conflict: take-server shows both sides and leaves the file untouched', async ({
    page,
  }) => {
    const raced = await seed('race target')
    await page.goto(`/n/${raced}?edit=1`)
    // The race only races if the page's own fetch landed first: the form
    // proves it, and the header shows the modified the page will send as base.
    await expect(page.getByTestId('edit-form')).toBeVisible()
    const uiBase = (await page.getByTestId('note-modified').textContent())!

    await racePatch(raced, uiBase, { entry_edit: { id: 1, text: 'server wins' } })

    await page.getByTestId('entry-text').fill('my draft')
    await page.getByTestId('entry-save').click()

    await expect(page.getByTestId('conflict-dialog')).toBeVisible()
    await expect(page.getByTestId('conflict-server')).toContainText('server wins')
    await expect(page.getByTestId('conflict-draft')).toContainText('my draft')

    await page.getByTestId('conflict-take-server').click()
    await expect(page.getByTestId('conflict-dialog')).toBeHidden()
    // Editor stays open and shows the server's version.
    await expect(page.getByTestId('entry-text')).toHaveValue('server wins')
    // The refused save never touched the file.
    expect(entryText(await getNote(raced))).toBe('server wins')
  })

  test('conflict: use-mine resends with the server modified and wins', async ({ page }) => {
    const raced = await seed('race target two')
    await page.goto(`/n/${raced}?edit=1`)
    await expect(page.getByTestId('edit-form')).toBeVisible()
    const uiBase = (await page.getByTestId('note-modified').textContent())!

    await racePatch(raced, uiBase, { entry_edit: { id: 1, text: 'server wins' } })
    const racedModified = (await getNote(raced) as { modified: string }).modified

    await page.getByTestId('entry-text').fill('my final text')
    await page.getByTestId('entry-save').click()
    await expect(page.getByTestId('conflict-dialog')).toBeVisible()

    // modified is second-granular: let the clock cross the boundary so the
    // winning write lands with a strictly newer timestamp.
    await page.waitForTimeout(1_050)
    await page.getByTestId('conflict-use-mine').click()
    await expect(page.getByTestId('conflict-dialog')).toBeHidden()
    await expect(page.getByText('saved', { exact: true })).toBeVisible()
    await expect
      .poll(async () => entryText(await getNote(raced)), { timeout: 2_000 })
      .toBe('my final text')
    // The winning save advanced modified past the value the dialog raced.
    const final = await getNote(raced)
    expect((final as { modified: string }).modified).not.toBe(racedModified)
  })

  test('state-select during an out-of-band change raises the same dialog', async ({ page }) => {
    const raced = await seed('state race note')
    await page.goto(`/n/${raced}`)
    await expect(page.getByTestId('state-select')).toContainText('inbox')
    const uiBase = (await page.getByTestId('note-modified').textContent())!

    await racePatch(raced, uiBase, { state: 'archived' })

    await page.getByTestId('state-select').click()
    await page.getByRole('option', { name: 'done', exact: true }).click()
    await expect(page.getByTestId('conflict-dialog')).toBeVisible()
    await expect(page.getByTestId('conflict-server')).toContainText('archived')
    await expect(page.getByTestId('conflict-draft')).toContainText('done')

    await page.getByTestId('conflict-take-server').click()
    await expect(page.getByTestId('conflict-dialog')).toBeHidden()
    // Adopted as local truth: the select now shows the server's state.
    await expect(page.getByTestId('state-select')).toContainText('archived')
  })
})

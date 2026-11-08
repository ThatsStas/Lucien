// Note list acceptance (card web-2): rows with the showing line, `/` search,
// state filter, j/k navigation, quick create, and the 3 s poll pulling in an
// out-of-band note. Seeding goes straight to the harness backend over HTTP,
// the curl style the card prescribes. Tests in one file run in declaration
// order on the single worker, and the notes accumulate: 3 seeds, then the
// keyboard-created note, then the out-of-band one.

import type { Page } from '@playwright/test'
import { expect } from '@playwright/test'

import { test } from './fixture'

import { BACKEND_PORT, E2E_TOKEN } from './server/global-setup'

async function seed(text: string): Promise<string> {
  const res = await fetch(`http://127.0.0.1:${BACKEND_PORT}/v1/notes`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${E2E_TOKEN}`,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ text }),
  })
  if (!res.ok) throw new Error(`seeding "${text}" failed: HTTP ${res.status}`)
  const body = (await res.json()) as { id: string }
  return body.id
}

function rows(page: Page) {
  return page.locator('[data-testid^="note-row-"]')
}

function searchBox(page: Page) {
  // Element Plus puts fallthrough attrs on the inner <input> itself, so the
  // testid is the input (same as the card-1 settings-token fields).
  return page.getByTestId('search-input')
}

test.describe('note list', () => {
  test('shows the seeded notes with the showing line', async ({ page }) => {
    // Sequential on purpose: the notes dir is a git repo and the backend
    // commits per write — concurrent creates race on .git/index.lock and the
    // loser answers 400 (observed against the binary; deviation report).
    const ids = []
    for (const text of ['milkshake recipe', 'return library books', 'call the dentist']) {
      ids.push(await seed(text))
    }
    await page.goto('/')
    for (const id of ids) {
      await expect(page.getByTestId(`note-row-${id}`)).toBeVisible()
    }
    await expect(page.getByTestId('list-status')).toHaveText('showing 3 of 3')
  })

  test('/ focuses search and narrows by substring', async ({ page }) => {
    await page.goto('/')
    await expect(rows(page)).toHaveCount(3)

    await page.keyboard.press('/')
    await page.keyboard.type('milk')
    // Card: ≤1 s after typing stops (300 ms debounce + one local fetch).
    await expect(rows(page)).toHaveCount(1, { timeout: 1_000 })
    await expect(rows(page).first()).toContainText('milkshake')

    // Escape blurs; the second Escape (on body) clears the query.
    await page.keyboard.press('Escape')
    await page.keyboard.press('Escape')
    await expect(rows(page)).toHaveCount(3)
  })

  test('state filter empties the inbox list and restores it', async ({ page }) => {
    await page.goto('/')
    await expect(rows(page)).toHaveCount(3)

    await page.getByTestId('state-filter').click()
    await page.getByRole('option', { name: 'todo', exact: true }).click()
    await expect(rows(page)).toHaveCount(0)
    await expect(page.getByText('no notes')).toBeVisible()

    await page.getByTestId('state-filter').click()
    await page.getByRole('option', { name: 'All' }).click()
    await expect(rows(page)).toHaveCount(3)
  })

  test('j and k move selection, clamp, and stay inert while typing', async ({ page }) => {
    await page.goto('/')
    await expect(rows(page)).toHaveCount(3)

    await page.keyboard.press('j')
    await page.keyboard.press('j')
    await expect(rows(page).nth(2)).toHaveAttribute('aria-current', 'true')
    await page.keyboard.press('k')
    await expect(rows(page).nth(1)).toHaveAttribute('aria-current', 'true')

    // k at the first row stays.
    await page.keyboard.press('k')
    await page.keyboard.press('k')
    await expect(rows(page).nth(0)).toHaveAttribute('aria-current', 'true')

    // j at the last row stays.
    await page.keyboard.press('j')
    await page.keyboard.press('j')
    await page.keyboard.press('j')
    await expect(rows(page).nth(2)).toHaveAttribute('aria-current', 'true')
    await page.keyboard.press('j')
    await expect(rows(page).nth(2)).toHaveAttribute('aria-current', 'true')

    // jjjk typed into search types letters and moves nothing: assert the
    // selection before the debounce can reset it, then the literal text.
    await searchBox(page).click()
    await page.keyboard.type('jjjk')
    await expect(rows(page).nth(2)).toHaveAttribute('aria-current', 'true')
    await expect(searchBox(page)).toHaveValue('jjjk')

    await searchBox(page).fill('')
    await expect(rows(page)).toHaveCount(3)
  })

  test('n opens the dialog and submitting creates a row with a toast', async ({ page }) => {
    await page.goto('/')
    await expect(rows(page)).toHaveCount(3)

    await page.keyboard.press('Escape') // blur anything focused
    await page.keyboard.press('n')
    const dialog = page.getByRole('dialog')
    await expect(dialog).toBeVisible()

    await page.getByTestId('new-note-text').fill('keyboard created')
    await page.getByTestId('new-note-submit').click()
    await expect(dialog).toBeHidden()
    await expect(page.getByText('note created')).toBeVisible()
    // Card: new row appears ≤4 s (immediate refetch on success).
    await expect(rows(page)).toHaveCount(4, { timeout: 4_000 })
    await expect(page.getByText('keyboard created')).toBeVisible()
  })

  test('an out-of-band note arrives on the poll with no reload', async ({ page }) => {
    await page.goto('/')
    await expect(rows(page)).toHaveCount(4)

    await seed('out of band note')
    // Card: appears ≤5 s; the poll fires every 3 s.
    await expect(rows(page)).toHaveCount(5, { timeout: 5_000 })
    await expect(page.getByText('out of band note')).toBeVisible()
  })
})

// Health footer against the real stack (card web-1 acceptance): ok, then
// unreachable after the harness backend is stopped, then ok again after it
// restarts. This spec owns the backend lifecycle; the suite is single-worker
// so no other spec races it.
import { expect } from '@playwright/test'

import { test } from './fixture'

import { ensureBackendUp, startBackend, stopBackend } from './server/backend'

test.describe('health footer', () => {
  test.afterAll(async () => {
    await ensureBackendUp()
  })

  test('tracks the backend across stop and restart', async ({ page }) => {
    await page.goto('/')
    const footer = page.getByRole('contentinfo')
    await expect(footer).toContainText('backend ok', { timeout: 10_000 })

    await stopBackend()
    await expect(footer).toContainText('backend unreachable', { timeout: 15_000 })

    await startBackend()
    await expect(footer).toContainText('backend ok', { timeout: 20_000 })
  })
})

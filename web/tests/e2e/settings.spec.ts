// Unauthenticated bootstrap flow (card web-1 acceptance): fresh context,
// no logged-in fixture.
import { expect, test } from '@playwright/test'

import { E2E_TOKEN } from './server/global-setup'

test.describe('settings bootstrap', () => {
  test('first visit lands on /settings; token entry, test, save, reload', async ({ page }) => {
    await page.goto('/')
    await expect(page).toHaveURL(/\/settings$/)
    await expect(page.getByTestId('settings-token')).toBeVisible()

    await page.getByTestId('settings-token').fill(E2E_TOKEN)
    await page.getByTestId('settings-test').click()
    await expect(page.getByTestId('settings-test-result')).toContainText('ok', {
      timeout: 10_000,
    })

    await page.getByTestId('settings-back').click()
    await expect(page.getByText('list ready')).toBeVisible({ timeout: 10_000 })

    // The store writes on change, so a reload must still be authenticated.
    await page.reload()
    await expect(page).toHaveURL(/\/$/)
    await expect(page.getByText('list ready')).toBeVisible({ timeout: 10_000 })
  })

  test('wrong token: inline 401 text, then one toast back to /settings', async ({ page }) => {
    await page.goto('/settings')
    await page.getByTestId('settings-token').fill('definitely-not-the-token')
    await page.getByTestId('settings-test').click()
    await expect(page.getByTestId('settings-test-result')).toContainText('401', {
      timeout: 10_000,
    })

    // A later authenticated call with the bad token routes back with a
    // single toast.
    await page.getByTestId('settings-back').click()
    await expect(page).toHaveURL(/\/settings$/, { timeout: 10_000 })
    await expect(page.getByText('authentication failed, check the token')).toHaveCount(1)
  })
})

// Logged-in fixture (FRONTEND-DESIGN.md 10): pre-fills lucien.settings
// before any page script runs. The unauthenticated flow uses plain `test`
// from @playwright/test and skips this file.

import { test as base, expect } from '@playwright/test'

import { E2E_TOKEN } from './server/global-setup'

export const test = base.extend({
  page: async ({ page }, use) => {
    await page.addInitScript(
      (token) => {
        window.localStorage.setItem(
          'lucien.settings',
          JSON.stringify({ token, serverUrl: '', pageSize: 50, theme: 'light' }),
        )
      },
      E2E_TOKEN,
    )
    await use(page)
  },
})

export { expect }

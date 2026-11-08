import { test, expect } from '@playwright/test'

// Placeholder smoke: the app mounts. Real specs arrive with the settings and
// health slices (cards 1 build 3 and 5); the harness config lands in build 4.
test('app mounts', async ({ page }) => {
  await page.goto('/')
  await expect(page.locator('#app')).toBeVisible()
})

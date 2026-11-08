import { fileURLToPath, URL } from 'node:url'

import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import vueDevTools from 'vite-plugin-vue-devtools'

// Serving contract (FRONTEND-DESIGN.md 3): dev and preview both proxy /v1 to
// the backend; nothing else in the app may reference an origin. The e2e
// harness starts its own backend on a fixed port and passes it via
// VITE_E2E_BACKEND_PORT (playwright.config webServer); a plain `pnpm
// preview` keeps the dev-machine default.
const proxyFor = (target: string) => ({ '/v1': target })

const devTarget = 'http://127.0.0.1:8188'
const previewTarget = process.env.VITE_E2E_BACKEND_PORT
  ? `http://127.0.0.1:${process.env.VITE_E2E_BACKEND_PORT}`
  : devTarget

// https://vite.dev/config/
export default defineConfig({
  plugins: [
    vue(),
    vueDevTools(),
  ],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: { proxy: proxyFor(devTarget) },
  preview: { proxy: proxyFor(previewTarget) },
})

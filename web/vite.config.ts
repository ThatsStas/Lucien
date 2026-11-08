import { fileURLToPath, URL } from 'node:url'

import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import vueDevTools from 'vite-plugin-vue-devtools'

// Serving contract (FRONTEND-DESIGN.md 3): one proxy table, used by dev and
// preview alike. The backend address is dev-machine truth; nothing else in
// the app may reference an origin.
const proxy = {
  '/v1': 'http://127.0.0.1:8188',
}

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
  server: { proxy },
  preview: { proxy },
})

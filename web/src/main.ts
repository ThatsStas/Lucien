import { createApp } from 'vue'
import { createPinia } from 'pinia'
import ElementPlus, { ElMessage } from 'element-plus'
import 'element-plus/dist/index.css'

import App from './App.vue'
import router from './router'
import {
  createClient,
  createUnauthorizedHandler,
  installAppApi,
  resetAuthHandler,
} from './api/app-api'
import { useSettingsStore } from './stores/settings'

import './assets/main.css'

const app = createApp(App)
app.use(createPinia())

// Reads lucien.settings synchronously; must happen before mount so the
// router guard and the client see loaded values on first paint.
const settings = useSettingsStore()

const unauthorized = createUnauthorizedHandler({
  toast: (message) =>
    ElMessage({
      message,
      type: 'error',
      // Once the toast is gone a new burst may alert again.
      onClose: () => resetAuthHandler(),
    }),
  goSettings: () => {
    void router.push({ name: 'settings' })
  },
})
installAppApi(
  createClient(() => ({ token: settings.token, serverUrl: settings.serverUrl })),
  unauthorized,
)

app.use(ElementPlus)
app.use(router)

app.mount('#app')

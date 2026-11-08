import { createRouter, createWebHistory } from 'vue-router'

import { useSettingsStore } from '@/stores/settings'
import ListView from '@/views/ListView.vue'
import NoteView from '@/views/NoteView.vue'
import SettingsView from '@/views/SettingsView.vue'

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes: [
    { path: '/', name: 'list', component: ListView },
    { path: '/n/:id', name: 'detail', component: NoteView, props: true },
    { path: '/settings', name: 'settings', component: SettingsView },
  ],
})

// Bootstrap rule (FRONTEND-DESIGN.md 6): first paint without a token lands
// on /settings and stays there.
router.beforeEach((to) => {
  const settings = useSettingsStore()
  if (!settings.isConfigured && to.name !== 'settings') {
    return { name: 'settings' }
  }
})

export default router

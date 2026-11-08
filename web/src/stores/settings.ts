// The only Pinia store (FRONTEND-DESIGN.md 6). One localStorage key,
// `lucien.settings`, one JSON object, read synchronously before mount and
// written on every change so there is no save-button state to get wrong.

import { defineStore } from 'pinia'
import { computed, ref, watch } from 'vue'

export const SETTINGS_KEY = 'lucien.settings'

export interface Settings {
  token: string
  serverUrl: string
  pageSize: number
  theme: 'light'
}

export const DEFAULT_SETTINGS: Settings = {
  token: '',
  serverUrl: '',
  pageSize: 50,
  theme: 'light',
}

function asString(value: unknown, fallback: string): string {
  return typeof value === 'string' ? value : fallback
}

function asPageSize(value: unknown): number {
  if (typeof value === 'number' && Number.isInteger(value) && value > 0 && value <= 1000) {
    return value
  }
  return DEFAULT_SETTINGS.pageSize
}

// Corrupt or hostile localStorage must never take down first paint.
function load(): Settings {
  let raw: string | null = null
  try {
    raw = localStorage.getItem(SETTINGS_KEY)
  } catch {
    return { ...DEFAULT_SETTINGS }
  }
  if (!raw) return { ...DEFAULT_SETTINGS }
  try {
    const parsed = JSON.parse(raw) as Record<string, unknown>
    return {
      token: asString(parsed.token, DEFAULT_SETTINGS.token),
      serverUrl: asString(parsed.serverUrl, DEFAULT_SETTINGS.serverUrl),
      pageSize: asPageSize(parsed.pageSize),
      theme: 'light',
    }
  } catch {
    return { ...DEFAULT_SETTINGS }
  }
}

export const useSettingsStore = defineStore('settings', () => {
  const initial = load()
  const token = ref(initial.token)
  const serverUrl = ref(initial.serverUrl)
  const pageSize = ref(initial.pageSize)
  const theme = ref<'light'>('light')

  const isConfigured = computed(() => token.value !== '')

  function persist() {
    try {
      localStorage.setItem(
        SETTINGS_KEY,
        JSON.stringify({
          token: token.value,
          serverUrl: serverUrl.value,
          pageSize: pageSize.value,
          theme: theme.value,
        }),
      )
    } catch {
      // Private-mode quota failures stay silent; the app still works for
      // this session, it just will not remember anything.
    }
  }
  watch([token, serverUrl, pageSize, theme], persist, { flush: 'sync' })

  return { token, serverUrl, pageSize, theme, isConfigured }
})

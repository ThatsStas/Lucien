import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'

import { SETTINGS_KEY, useSettingsStore } from '@/stores/settings'

beforeEach(() => {
  localStorage.clear()
  setActivePinia(createPinia())
})

describe('settings store defaults', () => {
  it('boots with an empty token and relative serverUrl', () => {
    const store = useSettingsStore()
    expect(store.token).toBe('')
    expect(store.serverUrl).toBe('')
    expect(store.pageSize).toBe(50)
    expect(store.theme).toBe('light')
    expect(store.isConfigured).toBe(false)
  })
})

describe('settings store load', () => {
  it('loads the persisted JSON object before first read', () => {
    localStorage.setItem(
      SETTINGS_KEY,
      JSON.stringify({ token: 'abc', serverUrl: 'http://10.0.0.5:8188', pageSize: 20, theme: 'light' }),
    )
    const store = useSettingsStore()
    expect(store.token).toBe('abc')
    expect(store.serverUrl).toBe('http://10.0.0.5:8188')
    expect(store.pageSize).toBe(20)
    expect(store.isConfigured).toBe(true)
  })

  it('falls back to defaults on corrupt JSON instead of throwing', () => {
    localStorage.setItem(SETTINGS_KEY, '{not json')
    const store = useSettingsStore()
    expect(store.token).toBe('')
    expect(store.pageSize).toBe(50)
  })

  it('ignores unknown keys and replaces wrong-typed values with defaults', () => {
    localStorage.setItem(
      SETTINGS_KEY,
      JSON.stringify({ token: 7, serverUrl: 'x', pageSize: 'many', theme: 'dark', junk: true }),
    )
    const store = useSettingsStore()
    expect(store.token).toBe('')
    expect(store.serverUrl).toBe('x')
    expect(store.pageSize).toBe(50)
    expect(store.theme).toBe('light')
  })

  it('tolerates a missing key entirely', () => {
    localStorage.removeItem(SETTINGS_KEY)
    const store = useSettingsStore()
    expect(store.token).toBe('')
  })
})

describe('settings store persistence', () => {
  it('writes on every change with no save step', () => {
    const store = useSettingsStore()
    store.token = 'written'
    // flush: 'sync' watcher means the mirror is current immediately
    const raw = localStorage.getItem(SETTINGS_KEY)
    expect(raw).not.toBeNull()
    expect(JSON.parse(raw!)).toEqual({
      token: 'written',
      serverUrl: '',
      pageSize: 50,
      theme: 'light',
    })
  })

  it('writes only the four documented keys', () => {
    const store = useSettingsStore()
    store.serverUrl = 'http://example.test'
    store.pageSize = 10
    const written = JSON.parse(localStorage.getItem(SETTINGS_KEY)!)
    expect(Object.keys(written).sort()).toEqual(['pageSize', 'serverUrl', 'theme', 'token'])
  })
})

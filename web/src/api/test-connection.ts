// Test connection for the settings screen (FRONTEND-DESIGN.md 6): health,
// then a one-item list probe, each answer surfaced as inline text, never a
// toast. Deliberately does not go through useAppApi so its 401 does not
// trigger the global redirect.

import { createClient, ApiError } from './client'

export interface TestResult {
  kind: 'ok' | 'unauthorized' | 'error'
  text: string
}

export async function testConnection(
  settings: { token: string; serverUrl: string },
  fetchImpl?: typeof fetch,
): Promise<TestResult> {
  const api = createClient(() => settings, fetchImpl)
  try {
    await api.health()
    await api.listNotes({ limit: 1 })
    return { kind: 'ok', text: 'ok' }
  } catch (err) {
    if (err instanceof ApiError) {
      if (err.status === 401)
        return { kind: 'unauthorized', text: `unauthorized (401): ${err.message}` }
      if (err.status === 0) return { kind: 'error', text: `network error: ${err.message}` }
      return { kind: 'error', text: `HTTP ${err.status}: ${err.message}` }
    }
    return { kind: 'error', text: `unexpected error: ${String(err)}` }
  }
}

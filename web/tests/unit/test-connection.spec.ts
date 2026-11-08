import { describe, expect, it, vi } from 'vitest'

import { testConnection } from '@/api/test-connection'

function stubFetch(
  health: { status: number; body: unknown },
  list: { status: number; body: unknown },
) {
  const calls: string[] = []
  const fn = vi.fn<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>(
    async (input) => {
      const url = String(input)
      calls.push(url)
      const spec = url.includes('/health') ? health : list
      return new Response(JSON.stringify(spec.body), {
        status: spec.status,
        headers: { 'Content-Type': 'application/json' },
      })
    },
  )
  return { fn, calls }
}

const settings = { token: 'tok', serverUrl: '' }

describe('testConnection', () => {
  it('reports ok when health and a one-item list both answer', async () => {
    const { fn, calls } = stubFetch({ status: 200, body: { status: 'ok' } }, { status: 200, body: { notes: [], total: 0 } })
    const result = await testConnection(settings, fn)
    expect(result).toMatchObject({ kind: 'ok' })
    expect(calls).toEqual(['/v1/health', '/v1/notes?limit=1'])
  })

  it('reports the 401 from the list probe', async () => {
    const { fn } = stubFetch({ status: 200, body: { status: 'ok' } }, { status: 401, body: { error: 'unauthorized' } })
    const result = await testConnection(settings, fn)
    expect(result.kind).toBe('unauthorized')
    expect(result.text).toContain('401')
  })

  it('reports a 401 from health without trying the list', async () => {
    const { fn, calls } = stubFetch({ status: 401, body: { error: 'unauthorized' } }, { status: 200, body: {} })
    const result = await testConnection(settings, fn)
    expect(result.kind).toBe('unauthorized')
    expect(calls).toEqual(['/v1/health'])
  })

  it('reports transport failure as network error', async () => {
    const fn = vi.fn<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>(
      async () => {
        throw new TypeError('Failed to fetch')
      },
    )
    const result = await testConnection(settings, fn)
    expect(result.kind).toBe('error')
    expect(result.text).toContain('network')
  })

  it('surfaces other server errors with status and message', async () => {
    const { fn } = stubFetch({ status: 500, body: { error: 'index busy' } }, { status: 200, body: {} })
    const result = await testConnection(settings, fn)
    expect(result.kind).toBe('error')
    expect(result.text).toContain('500')
    expect(result.text).toContain('index busy')
  })
})

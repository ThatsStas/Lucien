import { describe, expect, it, vi } from 'vitest'

import { ApiError, createClient } from '@/api/client'
import { createAppApi, createUnauthorizedHandler } from '@/api/app-api'

function makeHandler() {
  const toast = vi.fn<(message: string) => void>()
  const goSettings = vi.fn<() => void>()
  const handler = createUnauthorizedHandler({ toast, goSettings })
  return { handler, toast, goSettings }
}

describe('unauthorized handler', () => {
  it('toasts once and routes on a 401', () => {
    const { handler, toast, goSettings } = makeHandler()
    expect(handler.handle(new ApiError(401, 'unauthorized'))).toBe(true)
    expect(toast).toHaveBeenCalledTimes(1)
    expect(toast).toHaveBeenCalledWith(expect.stringContaining('token'))
    expect(goSettings).toHaveBeenCalledTimes(1)
  })

  it('does not stack toasts or re-route on queued 401s', () => {
    const { handler, toast, goSettings } = makeHandler()
    handler.handle(new ApiError(401, 'unauthorized'))
    handler.handle(new ApiError(401, 'unauthorized'))
    handler.handle(new ApiError(401, 'unauthorized'))
    expect(toast).toHaveBeenCalledTimes(1)
    expect(goSettings).toHaveBeenCalledTimes(1)
  })

  it('re-arms after the toast closes so a later burst fires again', () => {
    const { handler, toast } = makeHandler()
    handler.handle(new ApiError(401, 'x'))
    handler.reset()
    handler.handle(new ApiError(401, 'x'))
    expect(toast).toHaveBeenCalledTimes(2)
  })

  it('passes non-401 errors through untouched', () => {
    const { handler, toast, goSettings } = makeHandler()
    expect(handler.handle(new ApiError(404, 'gone'))).toBe(false)
    expect(handler.handle(new Error('boom'))).toBe(false)
    expect(toast).not.toHaveBeenCalled()
    expect(goSettings).not.toHaveBeenCalled()
  })
})

describe('app api wires the handler to real client rejections', () => {
  it('a 401 from the network arms the handler and still rejects', async () => {
    const { handler, toast, goSettings } = makeHandler()
    const fetchMock = vi.fn<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>(
      async () =>
        new Response(JSON.stringify({ error: 'unauthorized' }), {
          status: 401,
          headers: { 'Content-Type': 'application/json' },
        }),
    )
    const base = createClient(() => ({ token: 'bad', serverUrl: '' }), fetchMock)
    const api = createAppApi(base, handler)
    await expect(api.listNotes({})).rejects.toBeInstanceOf(ApiError)
    expect(toast).toHaveBeenCalledTimes(1)
    expect(goSettings).toHaveBeenCalledTimes(1)
  })

  it('leaves successful calls unwrapped', async () => {
    const { handler, toast } = makeHandler()
    const fetchMock = vi.fn<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>(
      async () =>
        new Response(JSON.stringify({ notes: [], total: 0 }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
    )
    const base = createClient(() => ({ token: 'good', serverUrl: '' }), fetchMock)
    const api = createAppApi(base, handler)
    await expect(api.listNotes({})).resolves.toEqual({ notes: [], total: 0 })
    expect(toast).not.toHaveBeenCalled()
  })
})

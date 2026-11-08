import { describe, expect, it, vi } from 'vitest'

import { ApiError, createClient } from '@/api/client'
import type { Note, Patch } from '@/api/types'

function settings(token = 'tok-1', serverUrl = '') {
  return () => ({ token, serverUrl })
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

type FetchStub = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>

function lastCall(fetchMock: ReturnType<typeof vi.fn<FetchStub>>) {
  const calls = fetchMock.mock.calls
  const call = calls[calls.length - 1]
  if (!call) throw new Error('fetch was never called')
  return { url: String(call[0]), init: (call[1] ?? {}) as RequestInit }
}

function testNote(over: Partial<Note> = {}): Note {
  return {
    id: '01NOTE',
    created: '2026-11-08T10:00:00Z',
    modified: '2026-11-08T10:05:00.123456Z',
    state: 'inbox',
    state_changed_at: '2026-11-08T10:00:00Z',
    tags: [],
    priority: 0,
    source: { kind: 'text', url: null },
    entries: [{ id: 0, ts: '2026-11-08T10:00:00Z', kind: 'text', text: 'hello' }],
    processing: {
      step: 'tag',
      status: 'pending',
      retry_count: 0,
      next_retry_at: null,
      last_error: null,
    },
    remind_at: null,
    defer_until: null,
    dedup: { url_hash: null, content_hash: 'deadbeef', resaved: [] },
    ...over,
  }
}

describe('client transport', () => {
  it('calls relative /v1 paths by default with the bearer header', async () => {
    const fetchMock = vi.fn<FetchStub>().mockResolvedValue(jsonResponse({ status: 'ok' }))
    const api = createClient(settings('tok-1'), fetchMock as unknown as typeof fetch)
    await api.health()
    const { url, init } = lastCall(fetchMock)
    expect(url).toBe('/v1/health')
    expect((init.headers as Record<string, string>)['Authorization']).toBe('Bearer tok-1')
  })

  it('honors the settings serverUrl override and trims its trailing slash', async () => {
    const fetchMock = vi.fn<FetchStub>().mockResolvedValue(jsonResponse({ status: 'ok' }))
    const api = createClient(
      settings('tok-1', 'http://10.0.0.5:8188/'),
      fetchMock as unknown as typeof fetch,
    )
    await api.health()
    expect(lastCall(fetchMock).url).toBe('http://10.0.0.5:8188/v1/health')
  })

  it('maps {error} bodies to ApiError with status and message', async () => {
    const fetchMock = vi.fn<FetchStub>().mockResolvedValue(jsonResponse({ error: 'invalid state foo' }, 400))
    const api = createClient(settings(), fetchMock as unknown as typeof fetch)
    const err = await api.listNotes({ state: ['foo' as never] }).catch((e) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).status).toBe(400)
    expect((err as ApiError).message).toBe('invalid state foo')
  })

  it('falls back to HTTP text when an error body is not JSON', async () => {
    const fetchMock = vi.fn<FetchStub>().mockResolvedValue(new Response('Bad Gateway', { status: 502 }))
    const api = createClient(settings(), fetchMock as unknown as typeof fetch)
    const err = await api.getNote('01NOPE').catch((e) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).status).toBe(502)
    expect((err as ApiError).message).toBe('Bad Gateway')
  })

  it('reports transport failure as ApiError status 0', async () => {
    const fetchMock = vi.fn<FetchStub>().mockRejectedValue(new TypeError('Failed to fetch'))
    const api = createClient(settings(), fetchMock as unknown as typeof fetch)
    const err = await api.getNote('01GONE').catch((e) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).status).toBe(0)
  })
})

describe('listNotes query string', () => {
  it('repeats array filters and omits empty parameters', async () => {
    const fetchMock = vi.fn<FetchStub>().mockResolvedValue(jsonResponse({ notes: [], total: 0 }))
    const api = createClient(settings(), fetchMock as unknown as typeof fetch)
    await api.listNotes({ state: ['inbox', 'todo'], q: 'milk', limit: 50 })
    const { url, init } = lastCall(fetchMock)
    expect(init.method ?? 'GET').toBe('GET')
    const qs = new URL(url, 'http://x').searchParams
    expect(qs.getAll('state')).toEqual(['inbox', 'todo'])
    expect(qs.get('q')).toBe('milk')
    expect(qs.get('limit')).toBe('50')
    expect(qs.has('tag')).toBe(false)
    expect(qs.has('modified_since')).toBe(false)
  })
})

describe('createText', () => {
  it('POSTs only the documented keys', async () => {
    const fetchMock = vi.fn<FetchStub>().mockResolvedValue(jsonResponse({ id: '01NEW' }, 201))
    const api = createClient(settings(), fetchMock as unknown as typeof fetch)
    const res = await api.createText({ text: 'milk', tags: ['a', 'b'] })
    expect(res).toEqual({ id: '01NEW' })
    const { url, init } = lastCall(fetchMock)
    expect(url).toBe('/v1/notes')
    expect(init.method).toBe('POST')
    expect(init.headers).toMatchObject({ 'Content-Type': 'application/json' })
    expect(JSON.parse(String(init.body))).toEqual({ text: 'milk', tags: ['a', 'b'] })
  })
})

describe('createAudio', () => {
  it('sends multipart FormData with the file and scalar fields', async () => {
    const fetchMock = vi.fn<FetchStub>().mockResolvedValue(jsonResponse({ id: '01AUD' }, 201))
    const api = createClient(settings(), fetchMock as unknown as typeof fetch)
    const file = new File(['audio-bytes'], 'voice.m4a', { type: 'audio/mp4' })
    const res = await api.createAudio(file, { state: 'inbox', priority: 2 })
    expect(res).toEqual({ id: '01AUD' })
    const { url, init } = lastCall(fetchMock)
    expect(url).toBe('/v1/notes')
    expect(init.method).toBe('POST')
    expect(init.body).toBeInstanceOf(FormData)
    const form = init.body as FormData
    const sent = form.get('file')
    expect(sent).toBeInstanceOf(File)
    expect((sent as File).name).toBe('voice.m4a')
    expect(form.get('priority')).toBe('2')
    expect(form.get('state')).toBe('inbox')
    // Boundary comes from the platform; hand-set Content-Type breaks it.
    expect(init.headers).not.toHaveProperty('Content-Type')
  })
})

describe('patchNote', () => {
  const patch: Patch = { base_modified: '2026-11-08T10:05:00.123456Z', state: 'todo' }

  it('resolves ok with the returned note on 200', async () => {
    const server = testNote({ state: 'todo' })
    const fetchMock = vi.fn<FetchStub>().mockResolvedValue(jsonResponse(server))
    const api = createClient(settings(), fetchMock as unknown as typeof fetch)
    const res = await api.patchNote('01NOTE', patch)
    expect(res).toEqual({ kind: 'ok', note: server })
    const { init } = lastCall(fetchMock)
    expect(init.method).toBe('PATCH')
    expect(JSON.parse(String(init.body))).toEqual(patch)
  })

  it('resolves conflict with the server note on 409, no rejection', async () => {
    const server = testNote({ state: 'done' })
    const fetchMock = vi.fn<FetchStub>().mockResolvedValue(jsonResponse(server, 409))
    const api = createClient(settings(), fetchMock as unknown as typeof fetch)
    const res = await api.patchNote('01NOTE', patch)
    expect(res).toEqual({ kind: 'conflict', note: server })
  })

  it('passes base_modified through byte-for-byte', async () => {
    const fetchMock = vi.fn<FetchStub>().mockResolvedValue(jsonResponse(testNote()))
    const api = createClient(settings(), fetchMock as unknown as typeof fetch)
    await api.patchNote('01NOTE', { base_modified: '2026-11-08T10:05:00.123456789Z' })
    expect(JSON.parse(String(lastCall(fetchMock).init.body))).toEqual({
      base_modified: '2026-11-08T10:05:00.123456789Z',
    })
  })
})

describe('the rest of the endpoint map', () => {
  it('getNote reads the full document', async () => {
    const note = testNote()
    const fetchMock = vi.fn<FetchStub>().mockResolvedValue(jsonResponse(note))
    const api = createClient(settings(), fetchMock as unknown as typeof fetch)
    await expect(api.getNote('01NOTE')).resolves.toEqual(note)
    expect(lastCall(fetchMock).url).toBe('/v1/notes/01NOTE')
  })

  it('appendText POSTs text and returns the note', async () => {
    const note = testNote()
    const fetchMock = vi.fn<FetchStub>().mockResolvedValue(jsonResponse(note))
    const api = createClient(settings(), fetchMock as unknown as typeof fetch)
    await api.appendText('01NOTE', 'more')
    const { url, init } = lastCall(fetchMock)
    expect(url).toBe('/v1/notes/01NOTE/append')
    expect(JSON.parse(String(init.body))).toEqual({ text: 'more' })
  })

  it('deleteNote returns the id', async () => {
    const fetchMock = vi.fn<FetchStub>().mockResolvedValue(jsonResponse({ id: '01NOTE' }))
    const api = createClient(settings(), fetchMock as unknown as typeof fetch)
    await expect(api.deleteNote('01NOTE')).resolves.toEqual({ id: '01NOTE' })
    expect(lastCall(fetchMock).init.method).toBe('DELETE')
  })
})

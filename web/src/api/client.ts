// The only module in the app that speaks HTTP (FRONTEND-DESIGN.md 1, 9).
// One exported function per backend endpoint; the Authorization header is
// attached here and nowhere else. getSettings and fetchImpl are injected so
// unit tests drive every branch without a network.

import type {
  CreateMeta,
  CreateTextRequest,
  ListParams,
  ListResult,
  Note,
  Patch,
  PatchResult,
} from './types'

export class ApiError extends Error {
  status: number
  // Parsed JSON body when the response carried one; the 409 path of
  // patchNote reads the current note from here.
  body?: unknown

  constructor(status: number, message: string, body?: unknown) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.body = body
  }
}

export interface Api {
  health(): Promise<{ status: string }>
  listNotes(p: ListParams): Promise<ListResult>
  getNote(id: string): Promise<Note>
  createText(r: CreateTextRequest): Promise<{ id: string }>
  createAudio(file: File, meta?: CreateMeta): Promise<{ id: string }>
  appendText(id: string, text: string): Promise<Note>
  patchNote(id: string, patch: Patch): Promise<PatchResult>
  deleteNote(id: string): Promise<{ id: string }>
}

type Settings = { token: string; serverUrl: string }

interface RequestOptions {
  query?: ListParams
  json?: unknown
  form?: FormData
}

export function createClient(
  getSettings: () => Settings,
  fetchImpl: typeof fetch = globalThis.fetch,
): Api {
  async function request<T>(method: string, path: string, opts: RequestOptions = {}): Promise<T> {
    const { token, serverUrl } = getSettings()
    const base = serverUrl.trim().replace(/\/+$/, '')
    const search = new URLSearchParams()
    if (opts.query) {
      for (const [key, value] of Object.entries(opts.query)) {
        if (value === undefined || value === null) continue
        const values = Array.isArray(value) ? value : [value]
        for (const v of values) search.append(key, String(v))
      }
    }
    const qs = search.toString()
    const url = base + path + (qs ? `?${qs}` : '')

    const headers: Record<string, string> = { Authorization: `Bearer ${token}` }
    let body: BodyInit | undefined
    if (opts.form) {
      body = opts.form // platform sets the multipart boundary itself
    } else if (opts.json !== undefined) {
      headers['Content-Type'] = 'application/json'
      body = JSON.stringify(opts.json)
    }

    let response: Response
    try {
      response = await fetchImpl(url.toString(), { method, headers, body })
    } catch (err) {
      throw new ApiError(0, err instanceof Error ? err.message : 'network failure')
    }

    const text = await response.text()
    if (!response.ok) {
      let message = text
      let body: unknown
      try {
        body = JSON.parse(text) as { error?: string }
        const parsed = body as { error?: string }
        if (parsed.error) message = parsed.error
      } catch {
        // non-JSON body: keep the raw text
      }
      throw new ApiError(response.status, message || response.statusText, body)
    }
    return JSON.parse(text) as T
  }

  // Drop undefined so the body carries only the documented keys; the backend
  // rejects unknown fields outright.
  function defined<T extends object>(obj: T): Partial<T> {
    return Object.fromEntries(Object.entries(obj).filter(([, v]) => v !== undefined)) as Partial<T>
  }

  return {
    health: () => request('GET', '/v1/health'),

    listNotes: (p) => request('GET', '/v1/notes', { query: defined(p) }),

    getNote: (id) => request('GET', `/v1/notes/${encodeURIComponent(id)}`),

    createText: (r) => request('POST', '/v1/notes', { json: defined(r) }),

    createAudio: (file, meta) => {
      const form = new FormData()
      form.append('file', file, file.name)
      if (meta?.state) form.append('state', meta.state)
      if (meta?.priority !== undefined) form.append('priority', String(meta.priority))
      // Backend parses this field as JSON (parseTagsField in server.go).
      if (meta?.tags?.length) form.append('tags', JSON.stringify(meta.tags))
      return request('POST', '/v1/notes', { form })
    },

    appendText: (id, text) =>
      request('POST', `/v1/notes/${encodeURIComponent(id)}/append`, { json: { text } }),

    async patchNote(id, patch): Promise<PatchResult> {
      try {
        const note = await request<Note>('PATCH', `/v1/notes/${encodeURIComponent(id)}`, {
          json: patch,
        })
        return { kind: 'ok', note }
      } catch (err) {
        if (err instanceof ApiError && err.status === 409) {
          // The 409 body is the full current note (DESIGN.md 5.7), carried
          // on ApiError.body by the request helper.
          return { kind: 'conflict', note: err.body as Note }
        }
        throw err
      }
    },

    deleteNote: (id) => request('DELETE', `/v1/notes/${encodeURIComponent(id)}`),
  }
}

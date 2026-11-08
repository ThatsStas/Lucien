// NoteView renders the full document from the API (card web-3 slice 1).
// The Api is injected as a prop so no test touches the network, and
// vue-router is mocked so navigation assertions stay cheap.

import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError, type Api } from '@/api/client'
import type { Note } from '@/api/types'

const push = vi.hoisted(() => vi.fn<(to: string) => void>())
vi.mock('vue-router', () => ({
  useRouter: () => ({ push }),
}))

import NoteView from '@/views/NoteView.vue'

function makeNote(over: Partial<Note> = {}): Note {
  return {
    id: '01ABC',
    created: '2026-11-07T10:00:00Z',
    modified: '2026-11-07T12:00:00Z',
    state: 'inbox',
    state_changed_at: '2026-11-07T10:00:00Z',
    tags: [{ name: 'groceries', by: 'agent' }],
    priority: 0,
    source: { kind: 'text', url: null },
    entries: [
      { id: 1, ts: '2026-11-07T10:00:00Z', kind: 'text', text: 'first line\nsecond line' },
      { id: 2, ts: '2026-11-07T10:05:00Z', kind: 'text', text: 'appended' },
    ],
    processing: {
      step: 'tag',
      status: 'pending',
      retry_count: 0,
      next_retry_at: null,
      last_error: null,
    },
    remind_at: null,
    defer_until: null,
    dedup: { url_hash: null, content_hash: 'abc', resaved: [] },
    ...over,
  }
}

function makeApi(note: Note | null): Api {
  return {
    getNote: vi.fn<(id: string) => Promise<Note>>(() =>
      note ? Promise.resolve(note) : Promise.reject(new ApiError(404, 'note not found')),
    ),
  } as unknown as Api
}

async function mountView(api: Api, id = '01ABC') {
  const wrapper = mount(NoteView, { props: { id, api } })
  await new Promise((r) => setTimeout(r, 0))
  return wrapper
}

describe('NoteView', () => {
  beforeEach(() => {
    push.mockClear()
  })

  it('fetches the note exactly once on mount', async () => {
    const api = makeApi(makeNote())
    await mountView(api)
    expect(api.getNote).toHaveBeenCalledTimes(1)
    expect(api.getNote).toHaveBeenCalledWith('01ABC')
  })

  it('shows the header fields verbatim', async () => {
    const wrapper = await mountView(makeApi(makeNote()))
    expect(wrapper.get('[data-testid="note-id"]').text()).toBe('01ABC')
    expect(wrapper.get('[data-testid="note-source"]').text()).toBe('text')
    expect(wrapper.get('[data-testid="note-created"]').text()).toBe('2026-11-07T10:00:00Z')
    expect(wrapper.get('[data-testid="note-modified"]').text()).toBe('2026-11-07T12:00:00Z')
  })

  it('renders entries as a timeline with kind, timestamp and full text', async () => {
    const wrapper = await mountView(makeApi(makeNote()))
    const first = wrapper.get('[data-testid="entry-1"]')
    expect(first.text()).toContain('text')
    expect(first.text()).toContain('2026-11-07T10:00:00Z')
    // Newlines survive: the text block must not collapse whitespace.
    expect(first.get('pre').element.textContent).toBe('first line\nsecond line')
    expect(wrapper.get('[data-testid="entry-2"]').text()).toContain('appended')
  })

  it('shows the full processing block, with retry data only when present', async () => {
    const resting = await mountView(makeApi(makeNote()))
    expect(resting.get('[data-testid="proc-step"]').text()).toBe('tag')
    expect(resting.get('[data-testid="proc-status"]').text()).toBe('pending')
    expect(resting.find('[data-testid="proc-retries"]').exists()).toBe(false)
    expect(resting.find('[data-testid="proc-error"]').exists()).toBe(false)

    const failed = await mountView(
      makeApi(
        makeNote({
          processing: {
            step: 'transcribe',
            status: 'failed',
            retry_count: 2,
            next_retry_at: null,
            last_error: 'whisper exploded at line 42',
          },
        }),
      ),
    )
    expect(failed.get('[data-testid="proc-retries"]').text()).toBe('2')
    expect(failed.get('[data-testid="proc-error"]').element.textContent?.trim()).toBe(
      'whisper exploded at line 42',
    )
  })

  it('shows a friendly not-found panel with a back link for 404', async () => {
    const wrapper = await mountView(makeApi(null))
    expect(wrapper.get('[data-testid="not-found"]').text()).toContain('note not found')
    expect(wrapper.get('[data-testid="not-found-back"]').attributes('role')).toBe('link')
    // Not a stack trace: the raw error text must not surface.
    expect(wrapper.text()).not.toContain('404')
  })

  it('treats a malformed-id 400 as not found too (acceptance 6)', async () => {
    const api = {
      getNote: vi.fn<(id: string) => Promise<Note>>(() =>
        Promise.reject(new ApiError(400, 'invalid id')),
      ),
    } as unknown as Api
    const wrapper = await mountView(api, '01BORUS')
    expect(wrapper.find('[data-testid="not-found"]').exists()).toBe(true)
  })

  it('reloads on demand: fetch-once means a second fetch only when asked', async () => {
    const api = makeApi(makeNote())
    const wrapper = await mountView(api)
    await wrapper.get('[data-testid="reload-button"]').trigger('click')
    await new Promise((r) => setTimeout(r, 0))
    expect(api.getNote).toHaveBeenCalledTimes(2)
  })

  it('surfaces unexpected failures as an error panel, not a crash', async () => {
    const api = {
      getNote: vi.fn<(id: string) => Promise<Note>>(() =>
        Promise.reject(new ApiError(500, 'index corrupt')),
      ),
    } as unknown as Api
    const wrapper = await mountView(api)
    expect(wrapper.get('[data-testid="note-error"]').text()).toContain('index corrupt')
  })
})

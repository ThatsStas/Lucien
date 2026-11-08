// useNoteList against a stubbed Api (card web-2 slice 1, FRONTEND-DESIGN.md 9).
// The scheduler is injected so polls advance by fake ticks, never by waiting.

import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'

import type { Api } from '@/api/client'
import type { ListParams, ListResult, Summary } from '@/api/types'
import { useNoteList, type Scheduler } from '@/composables/useNoteList'

function summary(id: string, modified: string, over: Partial<Summary> = {}): Summary {
  return {
    id,
    state: 'inbox',
    tags: [],
    priority: 0,
    created: modified,
    modified,
    source: { kind: 'text', url: null },
    processing: { step: 'tag', status: 'pending' },
    snippet: `snippet ${id}`,
    ...over,
  }
}

function fakeScheduler() {
  const intervals: Array<{ cb: () => void; ms: number }> = []
  const scheduler: Scheduler = {
    setInterval: (cb, ms) => {
      intervals.push({ cb, ms })
      return intervals.length
    },
    clearInterval: (handle) => {
      const i = Number(handle) - 1
      if (i >= 0) delete intervals[i]
    },
  }
  return {
    scheduler,
    tick(n = 1) {
      for (let i = 0; i < n; i++) for (const it of intervals) it?.cb()
    },
    get intervalCount() {
      return intervals.filter(Boolean).length
    },
  }
}

interface Stub {
  api: Api
  listNotes: ReturnType<typeof vi.fn<(p: ListParams) => Promise<ListResult>>>
}

function stubApi(...pages: ListResult[]): Stub {
  const listNotes = vi.fn<(p: ListParams) => Promise<ListResult>>()
  for (const page of pages) listNotes.mockResolvedValueOnce(page)
  listNotes.mockResolvedValue({ notes: [], total: 0 })
  return { api: { listNotes } as unknown as Api, listNotes }
}

// Lifecycle harness: the composable runs inside a real component so
// onMounted/onUnmounted fire, and exposes its state through the render.
function mountList(api: Api, opts = {}) {
  let state: ReturnType<typeof useNoteList> | null = null
  const Probe = defineComponent({
    setup() {
      state = useNoteList(api, opts)
      return () => h('div')
    },
  })
  const wrapper = mount(Probe)
  return { wrapper, get state() { return state! } }
}

const settle = () => new Promise((r) => setTimeout(r, 0))

describe('useNoteList', () => {
  it('fetches once on mount with the settings limit and exposes rows', async () => {
    const { api, listNotes } = stubApi({
      notes: [summary('b', '2026-11-08T10:01:00Z'), summary('a', '2026-11-08T10:00:00Z')],
      total: 2,
    })
    const { state } = mountList(api, { getLimit: () => 10 })
    await settle()
    expect(listNotes).toHaveBeenCalledTimes(1)
    expect(listNotes.mock.calls[0]![0]).toMatchObject({ limit: 10 })
    expect(state.rows.value.map((r) => r.id)).toEqual(['b', 'a'])
    expect(state.total.value).toBe(2)
    expect(state.loading.value).toBe(false)
  })

  it('polls on the interval with the cursor and merges by id, keeping modified desc order', async () => {
    const { api, listNotes } = stubApi(
      { notes: [summary('b', '2026-11-08T10:01:00Z'), summary('a', '2026-11-08T10:00:00Z')], total: 2 },
      { notes: [summary('b', '2026-11-08T10:05:00Z', { snippet: 'edited' }), summary('c', '2026-11-08T10:03:00Z')], total: 2 },
    )
    const clock = fakeScheduler()
    const { state } = mountList(api, { getLimit: () => 10, scheduler: clock.scheduler })
    await settle()

    clock.tick()
    await settle()
    expect(listNotes).toHaveBeenCalledTimes(2)
    expect(listNotes.mock.calls[1]![0]).toMatchObject({
      modified_since: '2026-11-08T10:01:00Z',
      limit: 10,
    })
    // c (10:03) ahead of edited b (10:05)? no: modified desc puts edited b first
    expect(state.rows.value.map((r) => r.id)).toEqual(['b', 'c', 'a'])
    expect(state.rows.value[0]!.snippet).toBe('edited')
  })

  it('keeps the selected note across a poll that inserts a newer row', async () => {
    const { api } = stubApi(
      { notes: [summary('b', '2026-11-08T10:01:00Z'), summary('a', '2026-11-08T10:00:00Z')], total: 2 },
      { notes: [summary('c', '2026-11-08T10:02:00Z')], total: 3 },
    )
    const clock = fakeScheduler()
    const { state } = mountList(api, { scheduler: clock.scheduler })
    await settle()
    state.selectIndex(1) // select 'a'
    expect(state.rows.value[state.selected.value]!.id).toBe('a')

    clock.tick()
    await settle()
    expect(state.rows.value.map((r) => r.id)).toEqual(['c', 'b', 'a'])
    expect(state.rows.value[state.selected.value]!.id).toBe('a')
  })

  it('failed poll sets offline without blanking rows, retries next tick, clears on success', async () => {
    const listNotes = vi.fn<(p: ListParams) => Promise<ListResult>>()
    listNotes.mockResolvedValueOnce({ notes: [summary('a', '2026-11-08T10:00:00Z')], total: 1 })
    listNotes.mockRejectedValueOnce(new Error('boom'))
    listNotes.mockResolvedValue({ notes: [], total: 1 })
    const clock = fakeScheduler()
    const { state } = mountList({ listNotes } as unknown as Api, { scheduler: clock.scheduler })
    await settle()
    expect(state.rows.value).toHaveLength(1)

    clock.tick()
    await settle()
    expect(state.offline.value).toBe(true)
    expect(state.rows.value).toHaveLength(1) // not blanked

    clock.tick()
    await settle()
    expect(state.offline.value).toBe(false)
  })

  it('state filter change refetches without cursor and resets selection', async () => {
    const { api, listNotes } = stubApi(
      { notes: [summary('b', '2026-11-08T10:01:00Z'), summary('a', '2026-11-08T10:00:00Z')], total: 2 },
      { notes: [summary('a', '2026-11-08T10:00:00Z')], total: 1 },
    )
    const { state } = mountList(api)
    await settle()
    state.selectIndex(1)

    state.filters.state = 'todo'
    await settle()
    expect(listNotes).toHaveBeenCalledTimes(2)
    expect(listNotes.mock.calls[1]![0]).toMatchObject({ state: ['todo'] })
    expect(listNotes.mock.calls[1]![0]!.modified_since).toBeUndefined()
    expect(state.selected.value).toBe(0)
  })

  it('search change feeds q to the next fetch', async () => {
    const { api, listNotes } = stubApi({ notes: [], total: 0 })
    const { state } = mountList(api)
    await settle()
    state.q.value = 'milk'
    await settle()
    expect(listNotes.mock.calls[1]![0]).toMatchObject({ q: 'milk' })
  })

  it('runs exactly one poll interval: one extra call per tick', async () => {
    const { api, listNotes } = stubApi({ notes: [], total: 0 })
    const clock = fakeScheduler()
    mountList(api, { scheduler: clock.scheduler })
    await settle()
    clock.tick(4)
    await settle()
    expect(clock.intervalCount).toBe(1)
    expect(listNotes).toHaveBeenCalledTimes(5) // 1 initial + 4 ticks
  })

  it('clamps selection at both ends and never wraps', async () => {
    const { api } = stubApi({
      notes: [summary('b', '2026-11-08T10:01:00Z'), summary('a', '2026-11-08T10:00:00Z')],
      total: 2,
    })
    const { state } = mountList(api)
    await settle()
    state.moveDown()
    state.moveDown()
    state.moveDown()
    expect(state.selected.value).toBe(1) // stays at last
    state.moveUp()
    state.moveUp()
    state.moveUp()
    expect(state.selected.value).toBe(0) // stays at first
  })

  it('exposes a showing X of Y line when the server has more rows than shown', async () => {
    const many = Array.from({ length: 3 }, (_, i) =>
      summary(`n${i}`, `2026-11-08T10:0${i}:00Z`),
    )
    const { api } = stubApi({ notes: many, total: 132 })
    const { state } = mountList(api, { getLimit: () => 3 })
    await settle()
    expect(state.showingLine.value).toBe('showing 3 of 132')
  })
})

// The note list's state and its 3 s cursor poll (FRONTEND-DESIGN.md 9, card
// web-2 slice 1). The scheduler is injected so unit tests advance ticks
// instead of waiting on real timers; the Api is injected so no test needs a
// network.

import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'

import type { Api } from '@/api/client'
import type { ListParams, State, Summary } from '@/api/types'

export interface Scheduler {
  setInterval(callback: () => void, ms: number): unknown
  clearInterval(handle: unknown): void
}

export interface UseNoteListOptions {
  pollMs?: number
  getLimit?: () => number
  scheduler?: Scheduler
}

const DEFAULT_POLL_MS = 3_000

// Card web-3 acceptance 4: Enter into a note and Esc back remounts the list;
// the selection rides through in module state, keyed by id so a row deleted
// server-side cannot haunt the index.
const selectionMemory: { id: string | null } = { id: null }

export function useNoteList(api: Api, opts: UseNoteListOptions = {}) {
  const pollMs = opts.pollMs ?? DEFAULT_POLL_MS
  const getLimit = opts.getLimit ?? (() => 50)
  // globalThis satisfies Scheduler structurally; the unknown-handle typing
  // is what the injection seam needs, not the timer ids.
  const scheduler: Scheduler = opts.scheduler ?? globalThis

  const rows = ref<Summary[]>([])
  const total = ref(0)
  const selected = ref(0)
  const loading = ref(false)
  const offline = ref(false)
  const q = ref('')
  const filters = reactive<{ state: State | '' }>({ state: '' })

  let cursor: string | null = null
  // A single restore attempt per instance: only the first fetch after mount
  // honors the remembered id (a filter change still resets to 0, section 9).
  let pendingRestore = selectionMemory.id !== null
  // Every full fetch starts a new generation; responses from older
  // generations (a filter changed mid-flight) are dropped rather than
  // merging stale rows into the new filter's list.
  let generation = 0

  function rememberSelection(): void {
    selectionMemory.id = rows.value[selected.value]?.id ?? null
  }

  function params(): ListParams {
    const p: ListParams = { limit: getLimit() }
    if (filters.state) p.state = [filters.state]
    if (q.value) p.q = q.value
    return p
  }

  // RFC3339 strings compare lexicographically within one offset format; the
  // server always emits UTC, and timestamps stay unparsed strings by
  // design (FRONTEND-DESIGN.md 5).
  function maxModified(seen: string | null, list: Summary[]): string | null {
    let m = seen
    for (const r of list) if (!m || r.modified > m) m = r.modified
    return m
  }

  // Mirrors the server's ORDER BY modified DESC, id DESC (index.go Query).
  function sortByServerOrder(list: Summary[]): Summary[] {
    return [...list].sort((a, b) =>
      a.modified !== b.modified ? (a.modified < b.modified ? 1 : -1) : a.id < b.id ? 1 : -1,
    )
  }

  function clamp(value: number): number {
    return Math.max(0, Math.min(value, Math.max(rows.value.length - 1, 0)))
  }

  async function fetchFull(): Promise<void> {
    const gen = ++generation
    loading.value = true
    try {
      const res = await api.listNotes(params())
      if (gen !== generation) return
      rows.value = sortByServerOrder(res.notes)
      total.value = res.total
      offline.value = false
      cursor = maxModified(null, res.notes)
      if (pendingRestore) {
        pendingRestore = false
        const i = rows.value.findIndex((r) => r.id === selectionMemory.id)
        selected.value = i >= 0 ? i : clamp(selected.value)
      }
      selected.value = clamp(selected.value)
      rememberSelection()
    } catch {
      if (gen !== generation) return
      offline.value = true
    } finally {
      if (gen === generation) loading.value = false
    }
  }

  async function poll(): Promise<void> {
    const gen = generation
    try {
      const p = params()
      if (cursor) p.modified_since = cursor
      const res = await api.listNotes(p)
      if (gen !== generation) return
      const selectedId = rows.value[selected.value]?.id
      const byId = new Map(rows.value.map((r) => [r.id, r]))
      for (const r of res.notes) byId.set(r.id, r)
      rows.value = sortByServerOrder([...byId.values()])
      if (selectedId) {
        const i = rows.value.findIndex((r) => r.id === selectedId)
        selected.value = i >= 0 ? i : clamp(selected.value)
      }
      cursor = maxModified(cursor, res.notes)
      offline.value = false
    } catch {
      if (gen !== generation) return
      offline.value = true
    }
  }

  watch([() => filters.state, q], () => {
    selected.value = 0
    cursor = null
    void fetchFull()
  })

  let handle: unknown
  onMounted(() => {
    void fetchFull()
    handle = scheduler.setInterval(() => void poll(), pollMs)
  })
  onUnmounted(() => scheduler.clearInterval(handle))

  const moveAndRemember = (delta: number): void => {
    selected.value = clamp(selected.value + delta)
    rememberSelection()
  }

  return {
    rows,
    total,
    selected,
    loading,
    offline,
    q,
    filters,
    selectIndex: (i: number) => {
      selected.value = clamp(i)
      rememberSelection()
    },
    moveDown: () => moveAndRemember(1),
    moveUp: () => moveAndRemember(-1),
    // Delete lands the row removal locally; the poll will never resurrect it
    // (deleted notes leave the server's cursor window entirely).
    removeRow: (id: string) => {
      if (!rows.value.some((r) => r.id === id)) return
      rows.value = rows.value.filter((r) => r.id !== id)
      total.value = Math.max(0, total.value - 1)
      selected.value = clamp(selected.value)
      rememberSelection()
    },
    refresh: () => {
      pendingRestore = false
      return fetchFull()
    },
    // Card web-2 acceptance pins the literal `showing 3 of 3`: the line
    // shows whenever there are rows, not only when more exist than shown.
    showingLine: computed(() =>
      total.value > 0 ? `showing ${rows.value.length} of ${total.value}` : '',
    ),
  }
}

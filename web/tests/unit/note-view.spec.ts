// NoteView renders the full document from the API (card web-3 slice 1).
// The Api is injected as a prop so no test touches the network, and
// vue-router is mocked so navigation assertions stay cheap.

import { mount, type VueWrapper } from '@vue/test-utils'
import { ElInputNumber, ElOption, ElSelect } from 'element-plus'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError, type Api } from '@/api/client'
import type { Note, Patch, PatchResult } from '@/api/types'
import { attachAppKeymap } from '@/lib/keymap'

const push = vi.hoisted(() => vi.fn<(to: string) => void>())
const routeQuery = vi.hoisted(() => ({ current: {} as Record<string, string> }))
vi.mock('vue-router', () => ({
  useRouter: () => ({ push }),
  useRoute: () => ({ query: routeQuery.current }),
}))

// Toast assertions need the message object, not the rendered DOM: keep every
// Element Plus export real but swap the ElMessage callable for a spy.
const toast = vi.hoisted(() => vi.fn<(opts: unknown) => void>())
vi.mock('element-plus', async (importOriginal) => {
  const original = await importOriginal<typeof import('element-plus')>()
  return { ...original, ElMessage: (opts: unknown) => toast(opts) }
})

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

function makeApi(note: Note | null, patchResult?: PatchResult): Api {
  return {
    getNote: vi.fn<(id: string) => Promise<Note>>(() =>
      note ? Promise.resolve(note) : Promise.reject(new ApiError(404, 'note not found')),
    ),
    patchNote: vi.fn<(id: string, p: Patch) => Promise<PatchResult>>(() =>
      Promise.resolve(patchResult ?? { kind: 'ok', note: note ?? makeNote() }),
    ),
    deleteNote: vi.fn<(id: string) => Promise<{ id: string }>>(() =>
      Promise.resolve({ id: '01ABC' }),
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

  it('offers exactly the six states in the header select, current one selected', async () => {
    const wrapper = await mountView(makeApi(makeNote()))
    const select = wrapper.findComponent(ElSelect)
    expect(select.exists()).toBe(true)
    expect(select.props('modelValue')).toBe('inbox')
    expect(wrapper.findAllComponents(ElOption).map((o) => o.props('value'))).toEqual([
      'inbox',
      'todo',
      'in_progress',
      'done',
      'someday',
      'archived',
    ])
  })

  it('patches with base_modified verbatim as last read', async () => {
    const api = makeApi(makeNote(), {
      kind: 'ok',
      note: makeNote({ state: 'todo', modified: '2026-11-07T13:00:00Z' }),
    })
    const wrapper = await mountView(api)
    wrapper.findComponent(ElSelect).vm.$emit('change', 'todo')
    await new Promise((r) => setTimeout(r, 0))
    expect(api.patchNote).toHaveBeenCalledWith('01ABC', {
      base_modified: '2026-11-07T12:00:00Z',
      state: 'todo',
    })
  })

  it('replaces the local note from the response and reuses its modified next time', async () => {
    const api = makeApi(makeNote(), {
      kind: 'ok',
      note: makeNote({ state: 'todo', modified: '2026-11-07T13:00:00Z' }),
    })
    const wrapper = await mountView(api)
    wrapper.findComponent(ElSelect).vm.$emit('change', 'todo')
    await new Promise((r) => setTimeout(r, 0))
    expect(wrapper.get('[data-testid="note-modified"]').text()).toBe('2026-11-07T13:00:00Z')
    expect(wrapper.findComponent(ElSelect).props('modelValue')).toBe('todo')

    wrapper.findComponent(ElSelect).vm.$emit('change', 'done')
    await new Promise((r) => setTimeout(r, 0))
    expect(api.patchNote).toHaveBeenLastCalledWith('01ABC', {
      base_modified: '2026-11-07T13:00:00Z',
      state: 'done',
    })
  })

  it('routes a conflict to a placeholder with the server modified and a reload', async () => {
    const api = makeApi(makeNote(), {
      kind: 'conflict',
      note: makeNote({ state: 'done', modified: '2026-11-07T14:00:00Z' }),
    })
    const wrapper = await mountView(api)
    wrapper.findComponent(ElSelect).vm.$emit('change', 'todo')
    await new Promise((r) => setTimeout(r, 0))
    const box = wrapper.get('[data-testid="conflict-placeholder"]')
    expect(box.text()).toContain('2026-11-07T14:00:00Z')

    await box.get('[data-testid="conflict-reload"]').trigger('click')
    await new Promise((r) => setTimeout(r, 0))
    expect(api.getNote).toHaveBeenCalledTimes(2)
    expect(wrapper.find('[data-testid="conflict-placeholder"]').exists()).toBe(false)
  })

  it('Esc returns to the list', async () => {
    const detach = attachAppKeymap()
    const wrapper = await mountView(makeApi(makeNote()))
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    expect(push).toHaveBeenCalledWith('/')
    wrapper.unmount()
    detach()
  })

  it('deletes after confirm: route home first, then the deleted message', async () => {
    const api = makeApi(makeNote())
    const wrapper = await mountView(api)
    await wrapper.get('[data-testid="delete-button"]').trigger('click')
    await new Promise((r) => setTimeout(r, 0))
    const confirm = wrapper.get('[data-testid="confirm-delete"]')
    expect(confirm.text()).toContain('01ABC')
    await confirm.trigger('click')
    await new Promise((r) => setTimeout(r, 0))
    expect(api.deleteNote).toHaveBeenCalledWith('01ABC')
    expect(push.mock.invocationCallOrder[0]).toBeLessThan(toast.mock.invocationCallOrder[0]!)
    expect(toast).toHaveBeenCalledWith(
      expect.objectContaining({ message: 'deleted 01ABC', type: 'success' }),
    )
    wrapper.unmount()
  })

  it('a server-side 404 on delete is an info toast, not an error', async () => {
    const api = makeApi(makeNote())
    ;(api.deleteNote as ReturnType<typeof vi.fn>).mockRejectedValue(
      new ApiError(404, 'gone already'),
    )
    const wrapper = await mountView(api)
    await wrapper.get('[data-testid="delete-button"]').trigger('click')
    await new Promise((r) => setTimeout(r, 0))
    await wrapper.get('[data-testid="confirm-delete"]').trigger('click')
    await new Promise((r) => setTimeout(r, 0))
    expect(push).toHaveBeenCalledWith('/')
    expect(toast).toHaveBeenCalledWith(
      expect.objectContaining({ message: 'deleted 01ABC', type: 'info' }),
    )
    wrapper.unmount()
  })
})

// Edit mode (card web-4 slice 1): e or edit-toggle opens the editor; one
// save ships entry_edit + priority + tags with base_modified held verbatim.
function textareaValue(wrapper: VueWrapper): string {
  return (wrapper.get('[data-testid="entry-text"]').element as HTMLTextAreaElement).value
}

describe('edit mode', () => {
  beforeEach(() => {
    push.mockClear()
    toast.mockClear()
    routeQuery.current = {}
  })

  it('edit-toggle opens the editor with the first entry loaded', async () => {
    const wrapper = await mountView(makeApi(makeNote()))
    await wrapper.get('[data-testid="edit-toggle"]').trigger('click')
    expect(wrapper.find('[data-testid="entry-select"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="entry-text"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="priority-input"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="entry-save"]').exists()).toBe(true)
    // The textarea starts at the selected entry's full text.
    expect(textareaValue(wrapper)).toBe('first line\nsecond line')
    wrapper.unmount()
  })

  it('e opens the editor through the app keymap', async () => {
    const detach = attachAppKeymap()
    const wrapper = await mountView(makeApi(makeNote()))
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'e' }))
    await new Promise((r) => setTimeout(r, 0))
    expect(wrapper.find('[data-testid="edit-form"]').exists()).toBe(true)
    wrapper.unmount()
    detach()
  })

  it('opens straight in edit mode when the route carries edit=1', async () => {
    routeQuery.current = { edit: '1' }
    const wrapper = await mountView(makeApi(makeNote()))
    expect(wrapper.find('[data-testid="edit-form"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('entry-select lists id and kind and loads the chosen entry text', async () => {
    const wrapper = await mountView(makeApi(makeNote()))
    await wrapper.get('[data-testid="edit-toggle"]').trigger('click')
    expect(wrapper.findAllComponents(ElOption).map((o) => o.props('label'))).toEqual([
      'inbox',
      'todo',
      'in_progress',
      'done',
      'someday',
      'archived',
      '1 text',
      '2 text',
    ])
    // The header state select is the first one; testids land on the inner
    // input, so drive the component instance, not the element.
    const entrySelect = wrapper.findAllComponents(ElSelect)[1] as unknown as VueWrapper
    entrySelect.vm.$emit('update:modelValue', 2)
    await new Promise((r) => setTimeout(r, 0))
    expect(textareaValue(wrapper)).toBe('appended')
    wrapper.unmount()
  })

  it('save sends one patch with entry_edit, priority, tags and base_modified verbatim', async () => {
    const saved = makeNote({ modified: '2026-11-07T13:00:00Z', priority: 2 })
    const api = makeApi(makeNote(), { kind: 'ok', note: saved })
    const wrapper = await mountView(api)
    await wrapper.get('[data-testid="edit-toggle"]').trigger('click')
    await wrapper.get('[data-testid="entry-text"]').setValue('v2 text')
    wrapper.findAllComponents(ElInputNumber)[0]!.vm.$emit('update:modelValue', 2)
    await wrapper.get('[data-testid="entry-save"]').trigger('click')
    await new Promise((r) => setTimeout(r, 0))
    expect(api.patchNote).toHaveBeenCalledTimes(1)
    expect(api.patchNote).toHaveBeenCalledWith('01ABC', {
      base_modified: '2026-11-07T12:00:00Z',
      entry_edit: { id: 1, text: 'v2 text' },
      priority: 2,
      tags: [{ name: 'groceries', by: 'agent' }],
    })
    wrapper.unmount()
  })

  it('ok replaces the note from the response, exits edit mode, toasts saved', async () => {
    const saved = makeNote({ modified: '2026-11-07T13:00:00Z', priority: 2 })
    const api = makeApi(makeNote(), { kind: 'ok', note: saved })
    const wrapper = await mountView(api)
    await wrapper.get('[data-testid="edit-toggle"]').trigger('click')
    await wrapper.get('[data-testid="entry-text"]').setValue('v2 text')
    await wrapper.get('[data-testid="entry-save"]').trigger('click')
    await new Promise((r) => setTimeout(r, 0))
    expect(wrapper.find('[data-testid="edit-form"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="note-modified"]').text()).toBe('2026-11-07T13:00:00Z')
    // The timeline renders the server's returned entry, not the local draft.
    expect(wrapper.get('[data-testid="entry-1"]').text()).toContain('first line')
    expect(toast).toHaveBeenCalledWith(expect.objectContaining({ message: 'saved' }))
    wrapper.unmount()
  })

  it('the next save forwards the modified the response just gave', async () => {
    const api = makeApi(makeNote(), {
      kind: 'ok',
      note: makeNote({ modified: '2026-11-07T13:00:00Z' }),
    })
    const wrapper = await mountView(api)
    await wrapper.get('[data-testid="edit-toggle"]').trigger('click')
    await wrapper.get('[data-testid="entry-save"]').trigger('click')
    await new Promise((r) => setTimeout(r, 0))
    await wrapper.get('[data-testid="edit-toggle"]').trigger('click')
    await wrapper.get('[data-testid="entry-save"]').trigger('click')
    await new Promise((r) => setTimeout(r, 0))
    expect(api.patchNote).toHaveBeenLastCalledWith(
      '01ABC',
      expect.objectContaining({ base_modified: '2026-11-07T13:00:00Z' }),
    )
    wrapper.unmount()
  })

  it('a save conflict keeps edit mode open and shows the conflict surface', async () => {
    const api = makeApi(makeNote(), {
      kind: 'conflict',
      note: makeNote({ modified: '2026-11-07T14:00:00Z' }),
    })
    const wrapper = await mountView(api)
    await wrapper.get('[data-testid="edit-toggle"]').trigger('click')
    await wrapper.get('[data-testid="entry-text"]').setValue('mine')
    await wrapper.get('[data-testid="entry-save"]').trigger('click')
    await new Promise((r) => setTimeout(r, 0))
    expect(wrapper.find('[data-testid="conflict-placeholder"]').exists()).toBe(true)
    // Draft survives: still editing, still my text in the textarea.
    expect(wrapper.find('[data-testid="edit-form"]').exists()).toBe(true)
    expect(textareaValue(wrapper)).toBe('mine')
    wrapper.unmount()
  })

  it('tags added in the editor ride the single save patch', async () => {
    const api = makeApi(makeNote())
    const wrapper = await mountView(api)
    await wrapper.get('[data-testid="edit-toggle"]').trigger('click')
    // The existing agent chip is visible but has no remove affordance.
    expect(wrapper.find('[data-testid="tag-remove-groceries"]').exists()).toBe(false)
    await wrapper.get('[data-testid="tag-input"]').setValue('urgent')
    await wrapper.get('[data-testid="tag-add"]').trigger('click')
    await wrapper.get('[data-testid="entry-save"]').trigger('click')
    await new Promise((r) => setTimeout(r, 0))
    expect(api.patchNote).toHaveBeenCalledWith(
      '01ABC',
      expect.objectContaining({
        tags: [
          { name: 'groceries', by: 'agent' },
          { name: 'urgent', by: 'human' },
        ],
      }),
    )
    wrapper.unmount()
  })

  it('cancel drops the draft without a patch', async () => {
    const api = makeApi(makeNote())
    const wrapper = await mountView(api)
    await wrapper.get('[data-testid="edit-toggle"]').trigger('click')
    await wrapper.get('[data-testid="entry-text"]').setValue('never saved')
    await wrapper.get('[data-testid="edit-cancel"]').trigger('click')
    expect(wrapper.find('[data-testid="edit-form"]').exists()).toBe(false)
    expect(api.patchNote).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('Esc in edit mode closes the editor instead of leaving the note', async () => {
    const detach = attachAppKeymap()
    const wrapper = await mountView(makeApi(makeNote()))
    await wrapper.get('[data-testid="edit-toggle"]').trigger('click')
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await new Promise((r) => setTimeout(r, 0))
    expect(wrapper.find('[data-testid="edit-form"]').exists()).toBe(false)
    expect(push).not.toHaveBeenCalled()
    wrapper.unmount()
    detach()
  })
})

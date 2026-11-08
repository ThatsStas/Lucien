// NoteList/NoteRow/ProcessingBadge element contract (card web-2 slice 2,
// FRONTEND-DESIGN.md 8). Rows are selected by index; the badge text is the
// phase-1 processing truth from section 5.

import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import NoteList from '@/components/NoteList.vue'
import type { Summary } from '@/api/types'

function summary(id: string, over: Partial<Summary> = {}): Summary {
  return {
    id,
    state: 'inbox',
    tags: [],
    priority: 0,
    created: '2026-11-08T10:00:00Z',
    modified: '2026-11-08T10:00:00Z',
    source: { kind: 'text', url: null },
    processing: { step: 'tag', status: 'pending' },
    snippet: `snippet ${id}`,
    ...over,
  }
}

function mountList(props: Partial<InstanceType<typeof NoteList>['$props']> = {}) {
  return mount(NoteList, {
    props: {
      rows: [summary('b'), summary('a')],
      selectedIndex: 0,
      loading: false,
      offline: false,
      showingLine: '',
      ...props,
    },
  })
}

describe('NoteList', () => {
  it('renders one row per note with the row testid and list semantics', () => {
    const wrapper = mountList()
    const list = wrapper.find('[data-testid="note-list"]')
    expect(list.attributes('role')).toBe('list')
    expect(list.findAll('[role="listitem"]')).toHaveLength(2)
    expect(wrapper.find('[data-testid="note-row-b"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="note-row-a"]').exists()).toBe(true)
  })

  it('marks only the selected row with aria-current', () => {
    const wrapper = mountList({ selectedIndex: 1 })
    expect(wrapper.find('[data-testid="note-row-b"]').attributes('aria-current')).toBeUndefined()
    expect(wrapper.find('[data-testid="note-row-a"]').attributes('aria-current')).toBe('true')
  })

  it('emits select with the clicked row index', async () => {
    const wrapper = mountList()
    await wrapper.find('[data-testid="note-row-a"]').trigger('click')
    expect(wrapper.emitted('select')).toEqual([[1]])
  })

  it('shows state, tags, source kind and the server snippet in the row', () => {
    const wrapper = mountList({
      rows: [
        summary('a', {
          state: 'todo',
          tags: [
            { name: 'groceries', by: 'agent' },
            { name: 'urgent', by: 'human' },
          ],
          snippet: 'buy oat milk',
        }),
      ],
    })
    const row = wrapper.find('[data-testid="note-row-a"]').text()
    expect(row).toContain('todo')
    expect(row).toContain('groceries')
    expect(row).toContain('urgent')
    expect(row).toContain('buy oat milk')
  })

  it('renders no rows and no offline marker in the happy case', () => {
    const wrapper = mountList({ rows: [] })
    expect(wrapper.findAll('[role="listitem"]')).toHaveLength(0)
    expect(wrapper.find('[data-testid="list-offline"]').exists()).toBe(false)
  })

  it('shows the offline marker without blanking rows', () => {
    const wrapper = mountList({ offline: true })
    expect(wrapper.find('[data-testid="list-offline"]').text()).toBe('offline')
    expect(wrapper.findAll('[role="listitem"]')).toHaveLength(2)
  })

  it('shows the showing X of Y line only when there is more to load', () => {
    expect(mountList().find('[data-testid="list-status"]').exists()).toBe(false)
    const wrapper = mountList({ showingLine: 'showing 2 of 132' })
    expect(wrapper.find('[data-testid="list-status"]').text()).toBe('showing 2 of 132')
  })
})

describe('ProcessingBadge', () => {
  it('says queued for tagging for a resting text note', () => {
    const wrapper = mountList()
    expect(wrapper.find('[data-testid="processing-badge"]').text()).toBe('queued for tagging')
  })

  it('shows step and status for audio notes', () => {
    const wrapper = mountList({
      rows: [
        summary('a', {
          source: { kind: 'audio', url: null },
          processing: { step: 'transcribe', status: 'running' },
        }),
      ],
    })
    expect(wrapper.find('[data-testid="processing-badge"]').text()).toBe('transcribe running')
  })

  it('renders no badge once processing is done', () => {
    const wrapper = mountList({
      rows: [
        summary('a', {
          processing: { step: 'none', status: 'pending' },
        }),
      ],
    })
    expect(wrapper.find('[data-testid="processing-badge"]').exists()).toBe(false)
  })
})

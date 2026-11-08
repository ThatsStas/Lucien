// ConflictDialog (card web-4 slice 3): the 409 side-by-side. Left column is
// the server note as the 409 returned it; right column is the patch that was
// about to go out. The dialog is chrome: it decides nothing, the parent
// handles both buttons. ElDialog teleports under attachTo, so lookups go
// through document, mirroring the delete confirm spec.

import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import ConflictDialog from '@/components/ConflictDialog.vue'
import type { ConflictDraft, Note } from '@/api/types'

const settle = () => new Promise((r) => setTimeout(r, 0))

function makeNote(over: Partial<Note> = {}): Note {
  return {
    id: '01ABC',
    created: '2026-11-07T10:00:00Z',
    modified: '2026-11-07T12:00:00Z',
    state: 'inbox',
    state_changed_at: '2026-11-07T10:00:00Z',
    tags: [],
    priority: 0,
    source: { kind: 'text', url: null },
    entries: [{ id: 1, ts: '2026-11-07T10:00:00Z', kind: 'text', text: 'server wins' }],
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

function serverNote(): Note {
  return makeNote({
    modified: '2026-11-07T14:00:00Z',
    priority: 2,
    tags: [{ name: 'agent-x', by: 'agent' }],
  })
}

async function mountDialog(draft: ConflictDraft) {
  const host = document.createElement('div')
  document.body.appendChild(host)
  const wrapper = mount(ConflictDialog, {
    props: { visible: true, server: serverNote(), draft },
    attachTo: host,
  })
  await settle()
  return {
    wrapper,
    text: (testId: string) => document.querySelector(`[data-testid="${testId}"]`)?.textContent,
    click: async (testId: string) => {
      document.querySelector<HTMLButtonElement>(`[data-testid="${testId}"]`)!.click()
      await settle()
    },
    cleanup: () => {
      wrapper.unmount()
      host.remove()
    },
  }
}

describe('ConflictDialog', () => {
  it('renders nothing while closed', () => {
    mount(ConflictDialog, {
      props: { visible: false, server: serverNote(), draft: {} },
    })
    expect(document.querySelector('[data-testid="conflict-dialog"]')).toBeNull()
  })

  it('left column is the server note: modified, entries, tags, priority', async () => {
    const { text, cleanup } = await mountDialog({})
    const left = text('conflict-server') ?? ''
    expect(left).toContain('2026-11-07T14:00:00Z')
    expect(left).toContain('server wins')
    expect(left).toContain('agent-x')
    expect(left).toContain('2')
    cleanup()
  })

  it('right column is what I was about to send', async () => {
    const { text, cleanup } = await mountDialog({
      entry_edit: { id: 1, text: 'my text' },
      priority: 3,
      tags: [{ name: 'urgent', by: 'human' }],
    })
    const right = text('conflict-draft') ?? ''
    expect(right).toContain('my text')
    expect(right).toContain('urgent')
    expect(right).toContain('3')
    cleanup()
  })

  it('a state-only draft shows just the state, no entry block', async () => {
    const { text, cleanup } = await mountDialog({ state: 'done' })
    expect(text('conflict-draft')).toContain('done')
    expect(document.querySelector('[data-testid="draft-entry"]')).toBeNull()
    cleanup()
  })

  it('the two buttons emit the two choices', async () => {
    const { wrapper, click, cleanup } = await mountDialog({ state: 'done' })
    await click('conflict-take-server')
    await click('conflict-use-mine')
    expect(wrapper.emitted('take-server')).toHaveLength(1)
    expect(wrapper.emitted('use-mine')).toHaveLength(1)
    cleanup()
  })
})

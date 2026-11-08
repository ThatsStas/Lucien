// TagEditor (card web-4 slice 2): human chips are yours, agent chips are
// the agent's. Removal and addition only ever touch by:"human" entries; the
// full array leaves through update:modelValue so the save can send it as a
// replacement. Duplicates die client-side with an inline hint.

import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import TagEditor from '@/components/TagEditor.vue'
import type { Tag } from '@/api/types'

function mountEditor(tags: Tag[]) {
  return mount(TagEditor, { props: { modelValue: tags } })
}

describe('TagEditor', () => {
  it('human chips carry a remove button, agent chips a lock and none', () => {
    const wrapper = mountEditor([
      { name: 'mine', by: 'human' },
      { name: 'agent-x', by: 'agent' },
    ])
    expect(wrapper.find('[data-testid="tag-chip-mine"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="tag-remove-mine"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="tag-remove-agent-x"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="tag-lock-agent-x"]').exists()).toBe(true)
  })

  it('removing a human chip emits the set without it', async () => {
    const wrapper = mountEditor([
      { name: 'mine', by: 'human' },
      { name: 'keep', by: 'human' },
    ])
    await wrapper.get('[data-testid="tag-remove-mine"]').trigger('click')
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual([[{ name: 'keep', by: 'human' }]])
  })

  it('agent tags survive removal untouched: the UI cannot produce or drop them', async () => {
    const wrapper = mountEditor([{ name: 'agent-x', by: 'agent' }])
    // No affordance to remove it, and adding keeps it in the emitted set.
    await wrapper.get('[data-testid="tag-input"]').setValue('fresh')
    await wrapper.get('[data-testid="tag-add"]').trigger('click')
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual([
      [
        { name: 'agent-x', by: 'agent' },
        { name: 'fresh', by: 'human' },
      ],
    ])
  })

  it('tag-add appends a human tag and clears the input', async () => {
    const wrapper = mountEditor([])
    await wrapper.get('[data-testid="tag-input"]').setValue('groceries')
    await wrapper.get('[data-testid="tag-add"]').trigger('click')
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual([
      [{ name: 'groceries', by: 'human' }],
    ])
    expect((wrapper.get('[data-testid="tag-input"]').element as HTMLInputElement).value).toBe('')
  })

  it('a duplicate name is rejected with an inline hint, no emit', async () => {
    const wrapper = mountEditor([{ name: 'dup', by: 'human' }])
    await wrapper.get('[data-testid="tag-input"]').setValue('dup')
    await wrapper.get('[data-testid="tag-add"]').trigger('click')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(wrapper.get('[data-testid="tag-hint"]').text()).toContain('dup')
  })

  it('duplicates against agent tags are rejected too, case and spaces forgiven', async () => {
    const wrapper = mountEditor([{ name: 'Agent-Tag', by: 'agent' }])
    await wrapper.get('[data-testid="tag-input"]').setValue('  agent-tag ')
    await wrapper.get('[data-testid="tag-add"]').trigger('click')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(wrapper.find('[data-testid="tag-hint"]').exists()).toBe(true)
  })

  it('empty or whitespace input adds nothing', async () => {
    const wrapper = mountEditor([])
    await wrapper.get('[data-testid="tag-input"]').setValue('   ')
    await wrapper.get('[data-testid="tag-add"]').trigger('click')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  })
})

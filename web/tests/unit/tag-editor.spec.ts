// TagEditor (card web-4 slice 2): human chips are yours, agent chips are
// the agent's. Removal and addition only ever touch by:"human" entries; the
// full array leaves through update:modelValue so the save can send it as a
// replacement. Duplicates die client-side with an inline hint.

import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import TagEditor from '@/components/TagEditor.vue'
import type { Tag, TagCount } from '@/api/types'

function mountEditor(tags: Tag[], vocabulary: TagCount[] = []) {
  return mount(TagEditor, { props: { modelValue: tags, vocabulary } })
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

// Tag suggestions (post-card addendum): the vocabulary prop drives a fuzzy
// dropdown so the human adopts the existing spelling. Keyboard and mouse are
// peers: every pick has both. An ignored dropdown never blocks plain adds.
// Engagement comes from click/keydown (jsdom never produces FocusEvents, so
// ElInput's focus emit is untestable there); hiding on blur dispatches a
// real FocusEvent, which is exactly what a browser does.
describe('TagEditor suggestions', () => {
  const vocab: TagCount[] = [
    { tag: 'wien', count: 5 },
    { tag: 'wiener-roast', count: 2 },
    { tag: 'kernel', count: 1 },
  ]

  function inputOf(wrapper: ReturnType<typeof mountEditor>) {
    return wrapper.get('[data-testid="tag-input"]')
  }

  async function engage(wrapper: ReturnType<typeof mountEditor>) {
    await inputOf(wrapper).trigger('click')
  }

  async function blur(wrapper: ReturnType<typeof mountEditor>) {
    inputOf(wrapper).element.dispatchEvent(new FocusEvent('blur'))
    await wrapper.vm.$nextTick()
  }

  it('typing shows ranked fuzzy matches, most-used first among equals', async () => {
    const wrapper = mountEditor([], vocab)
    await engage(wrapper)
    await inputOf(wrapper).setValue('we')
    const items = wrapper.findAll('[data-testid^="tag-suggest-"]')
    expect(items.map((i) => i.text())).toEqual(['wien', 'wiener-roast'])
  })

  it('clicking with nothing typed shows the most-used tags', async () => {
    const wrapper = mountEditor([], vocab)
    await engage(wrapper)
    expect(wrapper.find('[data-testid="tag-suggest"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="tag-suggest-wien"]').text()).toBe('wien')
  })

  it('ArrowDown then Enter adopts the existing name verbatim, not the typed text', async () => {
    const wrapper = mountEditor([], vocab)
    await engage(wrapper)
    await inputOf(wrapper).setValue('wen')
    await inputOf(wrapper).trigger('keydown', { key: 'ArrowDown' })
    await inputOf(wrapper).trigger('keydown', { key: 'Enter' })
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual([[{ name: 'wien', by: 'human' }]])
    expect((inputOf(wrapper).element as HTMLInputElement).value).toBe('')
  })

  it('ArrowDown wraps and ArrowUp walks back; Enter takes the highlighted row', async () => {
    const wrapper = mountEditor([], vocab)
    await engage(wrapper)
    await inputOf(wrapper).setValue('wi')
    // two items, start unhighlighted: Down lands on 0, Down wraps to 1
    await inputOf(wrapper).trigger('keydown', { key: 'ArrowDown' })
    await inputOf(wrapper).trigger('keydown', { key: 'ArrowDown' })
    await inputOf(wrapper).trigger('keydown', { key: 'ArrowUp' })
    await inputOf(wrapper).trigger('keydown', { key: 'Enter' })
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual([[{ name: 'wien', by: 'human' }]])
  })

  it('Tab accepts the highlighted suggestion instead of tabbing away', async () => {
    const wrapper = mountEditor([], vocab)
    await engage(wrapper)
    await inputOf(wrapper).setValue('knl')
    await inputOf(wrapper).trigger('keydown', { key: 'ArrowDown' })
    await inputOf(wrapper).trigger('keydown', { key: 'Tab' })
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual([[{ name: 'kernel', by: 'human' }]])
  })

  it('Esc closes the dropdown until typing resumes', async () => {
    const wrapper = mountEditor([], vocab)
    await engage(wrapper)
    await inputOf(wrapper).setValue('w')
    expect(wrapper.find('[data-testid="tag-suggest"]').exists()).toBe(true)
    await inputOf(wrapper).trigger('keydown', { key: 'Escape' })
    expect(wrapper.find('[data-testid="tag-suggest"]').exists()).toBe(false)
    await inputOf(wrapper).setValue('wi')
    expect(wrapper.find('[data-testid="tag-suggest"]').exists()).toBe(true)
  })

  it('Enter with nothing highlighted still adds the raw typed word', async () => {
    const wrapper = mountEditor([], vocab)
    await engage(wrapper)
    await inputOf(wrapper).setValue('fresh')
    await inputOf(wrapper).trigger('keydown', { key: 'Enter' })
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual([[{ name: 'fresh', by: 'human' }]])
  })

  it('clicking a suggestion adopts it verbatim', async () => {
    const wrapper = mountEditor([], vocab)
    await engage(wrapper)
    await inputOf(wrapper).setValue('wen')
    await wrapper.get('[data-testid="tag-suggest-wien"]').trigger('mousedown')
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual([[{ name: 'wien', by: 'human' }]])
  })

  it('tags the note already carries are never suggested back', async () => {
    const wrapper = mountEditor([{ name: 'Wien', by: 'human' }], vocab)
    await engage(wrapper)
    await inputOf(wrapper).setValue('wien')
    expect(wrapper.find('[data-testid="tag-suggest-wien"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="tag-suggest-wiener-roast"]').exists()).toBe(true)
  })

  it('a real blur retires the dropdown', async () => {
    const wrapper = mountEditor([], vocab)
    await engage(wrapper)
    await inputOf(wrapper).setValue('we')
    expect(wrapper.find('[data-testid="tag-suggest"]').exists()).toBe(true)
    await blur(wrapper)
    expect(wrapper.find('[data-testid="tag-suggest"]').exists()).toBe(false)
  })
})

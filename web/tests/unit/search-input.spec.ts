// SearchInput owns the 300 ms debounce and the first-press Escape blur
// (card web-2 slice 4, FRONTEND-DESIGN.md 7: second Escape clears, which the
// list's Escape binding does).

import { mount } from '@vue/test-utils'
import { ElInput } from 'element-plus'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import SearchInput from '@/components/SearchInput.vue'

function type(wrapper: ReturnType<typeof mount>, value: string): void {
  wrapper.findComponent(ElInput).vm.$emit('update:modelValue', value)
}

describe('SearchInput', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('emits nothing until 300 ms after the last keystroke', async () => {
    const wrapper = mount(SearchInput, { props: { modelValue: '' } })
    type(wrapper, 'm')
    type(wrapper, 'mi')
    type(wrapper, 'milk')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    vi.advanceTimersByTime(299)
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    vi.advanceTimersByTime(1)
    expect(wrapper.emitted('update:modelValue')).toEqual([['milk']])
    wrapper.unmount()
  })

  it('shows what the user typed before the debounce lands', async () => {
    const wrapper = mount(SearchInput, { props: { modelValue: '' } })
    type(wrapper, 'milk')
    await wrapper.vm.$nextTick()
    expect(wrapper.findComponent(ElInput).props('modelValue')).toBe('milk')
    wrapper.unmount()
  })

  it('follows an external modelValue change, which is how Escape clears it', async () => {
    const wrapper = mount(SearchInput, { props: { modelValue: 'stale' } })
    await wrapper.setProps({ modelValue: '' })
    expect(wrapper.findComponent(ElInput).props('modelValue')).toBe('')
    wrapper.unmount()
  })

  it('blurs on Escape without clearing the text', async () => {
    const host = document.createElement('div')
    document.body.appendChild(host)
    const wrapper = mount(SearchInput, { props: { modelValue: 'milk' }, attachTo: host })
    const input = wrapper.find('input').element
    input.focus()
    expect(document.activeElement).toBe(input)
    await wrapper.find('input').trigger('keydown', { key: 'Escape' })
    expect(document.activeElement).toBe(document.body)
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    wrapper.unmount()
    host.remove()
  })
})

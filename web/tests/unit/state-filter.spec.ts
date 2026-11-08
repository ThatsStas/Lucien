// StateFilter is chrome: All plus the six states (card web-2 slice 4).
// Interaction is e2e's job; this pins the option set and the testid.

import { mount } from '@vue/test-utils'
import { ElOption } from 'element-plus'
import { describe, expect, it } from 'vitest'

import StateFilter from '@/components/StateFilter.vue'

describe('StateFilter', () => {
  it('offers All and the six states under the state-filter testid', () => {
    const wrapper = mount(StateFilter, { props: { modelValue: '' } })
    expect(wrapper.find('[data-testid="state-filter"]').exists()).toBe(true)
    const labels = wrapper.findAllComponents(ElOption).map((o) => o.props('label'))
    expect(labels).toEqual(['All', 'inbox', 'todo', 'in_progress', 'done', 'someday', 'archived'])
  })

  it('carries the wire values, empty string for All', () => {
    const wrapper = mount(StateFilter, { props: { modelValue: '' } })
    const values = wrapper.findAllComponents(ElOption).map((o) => o.props('value'))
    expect(values).toEqual(['', 'inbox', 'todo', 'in_progress', 'done', 'someday', 'archived'])
  })
})

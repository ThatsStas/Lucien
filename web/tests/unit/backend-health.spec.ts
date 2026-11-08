import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'

import type { Api } from '@/api/client'
import { useBackendHealth } from '@/composables/useBackendHealth'

function makeApi(health: Api['health']): Api {
  return { health } as unknown as Api
}

const Harness = defineComponent({
  props: {
    api: { type: Object as () => Api, required: true },
  },
  setup(props) {
    const { state } = useBackendHealth(props.api, 10_000)
    return () => h('span', state.value)
  },
})

beforeEach(() => {
  vi.useFakeTimers()
})
afterEach(() => {
  vi.useRealTimers()
})

describe('useBackendHealth', () => {
  it('starts checking, goes ok on a good poll, stays ok across polls', async () => {
    const health = vi.fn<(p?: never) => Promise<{ status: string }>>(async () => ({ status: 'ok' }))
    const wrapper = mount(Harness, { props: { api: makeApi(health) } })
    await vi.waitFor(() => expect(wrapper.text()).toBe('ok'))
    expect(health).toHaveBeenCalledTimes(1)

    await vi.advanceTimersByTimeAsync(10_000)
    expect(wrapper.text()).toBe('ok')
    expect(health).toHaveBeenCalledTimes(2)
  })

  it('flips to unreachable when a poll fails and back when it recovers', async () => {
    let alive = true
    const health = vi.fn<(p?: never) => Promise<{ status: string }>>(async () => {
      if (!alive) throw new Error('network down')
      return { status: 'ok' }
    })
    const wrapper = mount(Harness, { props: { api: makeApi(health) } })
    await vi.waitFor(() => expect(wrapper.text()).toBe('ok'))

    alive = false
    await vi.advanceTimersByTimeAsync(10_000)
    expect(wrapper.text()).toBe('unreachable')

    alive = true
    await vi.advanceTimersByTimeAsync(10_000)
    expect(wrapper.text()).toBe('ok')
  })
})

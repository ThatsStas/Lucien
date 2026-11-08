// NewNoteDialog drives createText itself and reports success upward so the
// list can refetch (card web-2 slice 4). The app-api module is mocked; the
// client, ElMessage and ElDialog run for real. ElDialog teleports its content
// to body, so the assertions go through document.

import { mount } from '@vue/test-utils'
import { ElMessage } from 'element-plus'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import NewNoteDialog from '@/components/NewNoteDialog.vue'
import { ApiError } from '@/api/client'

const { createText } = vi.hoisted(() => ({
  createText: vi.fn<(r: { text: string }) => Promise<{ id: string }>>(),
}))

vi.mock('@/api/app-api', () => ({
  useAppApi: () => ({ createText }),
}))

async function mountDialog() {
  const host = document.createElement('div')
  document.body.appendChild(host)
  // ElDialog renders lazily on the modelValue false -> true transition; the
  // app only ever opens it that way, so the test drives the same path.
  const wrapper = mount(NewNoteDialog, { props: { visible: false }, attachTo: host })
  await wrapper.setProps({ visible: true })
  await new Promise((r) => setTimeout(r, 0))
  return {
    wrapper,
    text: () => document.querySelector<HTMLTextAreaElement>('[data-testid="new-note-text"]')!,
    submit: () => document.querySelector<HTMLButtonElement>('[data-testid="new-note-submit"]')!,
    cleanup: () => {
      wrapper.unmount()
      host.remove()
      ElMessage.closeAll()
    },
  }
}

function typeText(value: string): void {
  // ElInput textarea mode puts fallthrough attrs on the <textarea> itself.
  const el = document.querySelector<HTMLTextAreaElement>('[data-testid="new-note-text"]')!
  el.value = value
  el.dispatchEvent(new Event('input', { bubbles: true }))
}

async function flush(): Promise<void> {
  await new Promise((r) => setTimeout(r, 0))
}

describe('NewNoteDialog', () => {
  beforeEach(() => {
    createText.mockReset()
  })
  afterEach(() => {
    document.body.innerHTML = ''
  })

  it('creates the note, reports it, and closes on submit', async () => {
    createText.mockResolvedValue({ id: '01ABC' })
    const { wrapper, submit, cleanup } = await mountDialog()
    typeText('keyboard created')
    await flush() // let :disabled clear before clicking
    submit().click()
    await flush()
    expect(createText).toHaveBeenCalledWith({ text: 'keyboard created' })
    expect(wrapper.emitted('created')).toHaveLength(1)
    expect(wrapper.emitted('update:visible')).toEqual([[false]])
    cleanup()
  })

  it('keeps the dialog open and reports the failure when createText rejects', async () => {
    createText.mockRejectedValue(new ApiError(500, 'backend exploded'))
    const { wrapper, submit, cleanup } = await mountDialog()
    typeText('doomed')
    await flush()
    submit().click()
    await flush()
    expect(wrapper.emitted('created')).toBeUndefined()
    expect(wrapper.emitted('update:visible')).toBeUndefined()
    cleanup()
  })

  it('disables submit while the text is blank', async () => {
    const { submit, cleanup } = await mountDialog()
    expect(submit().disabled).toBe(true)
    submit().click()
    expect(createText).not.toHaveBeenCalled()
    cleanup()
  })
})

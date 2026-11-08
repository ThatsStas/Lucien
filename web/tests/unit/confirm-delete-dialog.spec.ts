// The shared delete confirmation (card web-3 slice 3). It is chrome only:
// the caller runs deleteNote; this dialog names the id and emits confirm.
// ElDialog teleports to body, so lookups go through document.

import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import ConfirmDeleteDialog from '@/components/ConfirmDeleteDialog.vue'

const settle = () => new Promise((r) => setTimeout(r, 0))

async function mountDialog(noteId: string) {
  const host = document.createElement('div')
  document.body.appendChild(host)
  const wrapper = mount(ConfirmDeleteDialog, {
    props: { visible: false, noteId },
    attachTo: host,
  })
  await wrapper.setProps({ visible: true })
  await settle()
  return {
    wrapper,
    button: () => document.querySelector<HTMLButtonElement>('[data-testid="confirm-delete"]'),
    cleanup: () => {
      wrapper.unmount()
      host.remove()
    },
  }
}

describe('ConfirmDeleteDialog', () => {
  it('renders nothing while closed', () => {
    const wrapper = mount(ConfirmDeleteDialog, { props: { visible: false, noteId: '01ABC' } })
    expect(document.querySelector('[data-testid="confirm-delete"]')).toBeNull()
    expect(wrapper.find('[data-testid="confirm-delete"]').exists()).toBe(false)
  })

  it('names the note id in the confirm button and emits confirm on click', async () => {
    const { wrapper, button, cleanup } = await mountDialog('01ABC')
    const confirm = button()
    expect(confirm).not.toBeNull()
    expect(confirm!.textContent).toContain('01ABC')
    confirm!.click()
    await settle()
    expect(wrapper.emitted('confirm')).toHaveLength(1)
    cleanup()
  })
})

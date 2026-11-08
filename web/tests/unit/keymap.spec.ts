// Keymap dispatch and guard rules from FRONTEND-DESIGN.md 7 (card web-2
// slice 3). The keymap is a factory with injected deps so the guard matrix is
// testable without a mounted app; it must not import Element Plus.

import { beforeEach, describe, expect, it, vi } from 'vitest'

import { createKeymap } from '@/lib/keymap'

function keydown(key: string, init: KeyboardEventInit = {}): KeyboardEvent {
  return new KeyboardEvent('keydown', { key, cancelable: true, bubbles: true, ...init })
}

// A synthetic event whose target we control, since jsdom assigns targets only
// through real DOM dispatch.
function eventFrom(key: string, target: Element, init: KeyboardEventInit = {}): KeyboardEvent {
  const ev = keydown(key, init)
  Object.defineProperty(ev, 'target', { value: target })
  Object.defineProperty(ev, 'currentTarget', { value: target })
  return ev
}

describe('createKeymap', () => {
  let km: ReturnType<typeof createKeymap>
  let active: Element | null
  let dialogOpen: boolean

  beforeEach(() => {
    document.body.innerHTML = ''
    active = document.body
    dialogOpen = false
    km = createKeymap({
      activeElement: () => active,
      isDialogOpen: () => dialogOpen,
    })
  })

  it('dispatches j to the registered handler and prevents the default', () => {
    const j = vi.fn<() => void>()
    km.push({ j })
    const ev = eventFrom('j', document.body)
    km.dispatch(ev)
    expect(j).toHaveBeenCalledTimes(1)
    expect(ev.defaultPrevented).toBe(true)
  })

  it.each([
    ['input', () => document.body.appendChild(document.createElement('input'))],
    ['textarea', () => document.body.appendChild(document.createElement('textarea'))],
    ['select', () => document.body.appendChild(document.createElement('select'))],
    [
      'contenteditable',
      () => {
        const el = document.createElement('div')
        // setAttribute, not el.contentEditable = 'true': jsdom does not
        // reflect that IDL property to the attribute, browsers do.
        el.setAttribute('contenteditable', 'true')
        return document.body.appendChild(el)
      },
    ],
    ['button', () => document.body.appendChild(document.createElement('button'))],
  ])('is inert while %s holds focus', (_name, make) => {
    const el = make()
    active = el
    const j = vi.fn<() => void>()
    km.push({ j })
    km.dispatch(eventFrom('j', el))
    expect(j).not.toHaveBeenCalled()
  })

  it('is inert while a dialog is open, for every key', () => {
    const j = vi.fn<() => void>()
    const slash = vi.fn<() => void>()
    km.push({ j, '/': slash })
    dialogOpen = true
    km.dispatch(eventFrom('j', document.body))
    km.dispatch(eventFrom('/', document.body))
    expect(j).not.toHaveBeenCalled()
    expect(slash).not.toHaveBeenCalled()
  })

  it('ignores events with modifier keys', () => {
    const j = vi.fn<() => void>()
    km.push({ j })
    km.dispatch(eventFrom('j', document.body, { ctrlKey: true }))
    km.dispatch(eventFrom('j', document.body, { metaKey: true }))
    km.dispatch(eventFrom('j', document.body, { altKey: true }))
    expect(j).not.toHaveBeenCalled()
  })

  it('leaves unbound keys alone', () => {
    const j = vi.fn<() => void>()
    km.push({ j })
    const ev = eventFrom('x', document.body)
    km.dispatch(ev)
    expect(j).not.toHaveBeenCalled()
    expect(ev.defaultPrevented).toBe(false)
  })

  it('dispatches to the topmost table that binds the key, and pop reveals the one below', () => {
    const jLower = vi.fn<() => void>()
    const jUpper = vi.fn<() => void>()
    km.push({ j: jLower })
    const upper = { j: jUpper }
    km.push(upper)
    km.dispatch(eventFrom('j', document.body))
    expect(jUpper).toHaveBeenCalledTimes(1)
    expect(jLower).not.toHaveBeenCalled()

    km.pop(upper)
    km.dispatch(eventFrom('j', document.body))
    expect(jLower).toHaveBeenCalledTimes(1)
  })

  it('attach wires keydown on the injected event target and detaches', () => {
    const listeners: Array<(ev: KeyboardEvent) => void> = []
    const target = {
      addEventListener: (_type: string, cb: (ev: KeyboardEvent) => void) => listeners.push(cb),
      removeEventListener: (_type: string, cb: (ev: KeyboardEvent) => void) => {
        const i = listeners.indexOf(cb)
        if (i >= 0) listeners.splice(i, 1)
      },
    }
    const attached = createKeymap({
      activeElement: () => active,
      isDialogOpen: () => dialogOpen,
      eventTarget: target,
    })
    const j = vi.fn<() => void>()
    attached.push({ j })
    const detach = attached.attach()

    listeners[0]!(eventFrom('j', document.body))
    expect(j).toHaveBeenCalledTimes(1)

    detach()
    expect(listeners).toHaveLength(0)
  })
})

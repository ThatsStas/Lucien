// The global key dispatch (FRONTEND-DESIGN.md 7, card web-2 slice 3).
// Element Plus must never be imported here: the guard matrix has to be
// debuggable with nothing but the DOM, per the section 8 hand-rolled list.

import { onMounted, onUnmounted } from 'vue'

export type KeyName = 'j' | 'k' | 'Enter' | 'e' | 'n' | 'd' | '/' | 'Escape'
export type KeyHandlers = Partial<Record<KeyName, () => void>>

export interface KeymapDeps {
  activeElement?: () => Element | null
  isDialogOpen?: () => boolean
  eventTarget?: {
    addEventListener(type: string, callback: (ev: KeyboardEvent) => void): void
    removeEventListener(type: string, callback: (ev: KeyboardEvent) => void): void
  }
}

// Focus in any of these makes vim keys inert (section 7).
const TYPING_SELECTOR = 'input, textarea, select, [contenteditable]:not([contenteditable="false"])'
// The wider rule: the global handler only acts when focus sits on body or a
// non-focusable element, so a focused button swallows j/k by design.
const FOCUSABLE_SELECTOR = `${TYPING_SELECTOR}, button, a[href], [tabindex]`

// Element Plus dialogs keep their own Escape handling; the global handler is
// blind while any dialog is open. Dialog components bracket themselves in the
// lock so the keymap can see what ElDialog will not announce.
let dialogLocks = 0
export function lockDialogs(): void {
  dialogLocks += 1
}
export function unlockDialogs(): void {
  if (dialogLocks > 0) dialogLocks -= 1
}
export function dialogsOpen(): boolean {
  return dialogLocks > 0
}

export function createKeymap(deps: KeymapDeps = {}) {
  const activeElement =
    deps.activeElement ?? (() => (typeof document === 'undefined' ? null : document.activeElement))
  const isDialogOpen = deps.isDialogOpen ?? dialogsOpen
  const eventTarget = deps.eventTarget ?? (typeof window === 'undefined' ? undefined : window)

  const stack: KeyHandlers[] = []

  function dispatch(ev: KeyboardEvent): void {
    if (ev.ctrlKey || ev.metaKey || ev.altKey) return
    if (isDialogOpen()) return
    const el = activeElement()
    if (el && el.matches(FOCUSABLE_SELECTOR)) return
    // Topmost table (most recently pushed, e.g. the active dialog's view)
    // wins the key; below it sits the list view's table.
    for (let i = stack.length - 1; i >= 0; i--) {
      const handler = stack[i]?.[ev.key as KeyName]
      if (handler) {
        ev.preventDefault()
        handler()
        return
      }
    }
  }

  function onKeydown(ev: KeyboardEvent): void {
    dispatch(ev)
  }

  return {
    push(handlers: KeyHandlers): void {
      stack.push(handlers)
    },
    pop(handlers: KeyHandlers): void {
      const i = stack.indexOf(handlers)
      if (i >= 0) stack.splice(i, 1)
    },
    dispatch,
    attach(): () => void {
      eventTarget?.addEventListener('keydown', onKeydown)
      return () => eventTarget?.removeEventListener('keydown', onKeydown)
    },
  }
}

export type Keymap = ReturnType<typeof createKeymap>

// The app-wide instance. Views that create their own keymap would violate
// the once-only rule in section 7.
const appKeymap = createKeymap()

export function attachAppKeymap(): () => void {
  return appKeymap.attach()
}

// Views register their keys for as long as they are mounted.
export function useKeys(handlers: KeyHandlers): void {
  onMounted(() => appKeymap.push(handlers))
  onUnmounted(() => appKeymap.pop(handlers))
}

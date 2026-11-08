// Polling for the footer's backend line (FRONTEND-DESIGN.md: App shell,
// card web-1 build 5). Pure state machine over an injected Api so unit
// tests never wait on real timers or the network.

import { onMounted, onUnmounted, ref, type Ref } from 'vue'

import type { Api } from '@/api/client'

export type HealthState = 'checking' | 'ok' | 'unreachable'

export function useBackendHealth(api: Api, intervalMs = 10_000): { state: Ref<HealthState> } {
  const state = ref<HealthState>('checking')
  let timer: ReturnType<typeof setInterval> | undefined

  async function check(): Promise<void> {
    try {
      await api.health()
      state.value = 'ok'
    } catch {
      state.value = 'unreachable'
    }
  }

  onMounted(() => {
    void check()
    timer = setInterval(() => void check(), intervalMs)
  })
  onUnmounted(() => {
    if (timer) clearInterval(timer)
  })

  return { state }
}

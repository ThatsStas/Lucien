<script setup lang="ts">
// Phase-1 processing truth (FRONTEND-DESIGN.md 5, card web-3 slice 4).
// Text notes park at tag/pending forever, so that rest state reads as a
// muted "queued for tagging" — never a spinner. Audio gets honest words for
// the transcribe wait. Anything else shows raw step + status; the badge
// never invents progress the file does not hold.
import { computed } from 'vue'

import type { ProcStep, SourceKind } from '@/api/types'

const props = defineProps<{
  sourceKind: SourceKind
  processing: { step: ProcStep; status: string; last_error?: string | null }
}>()

// Row summaries carry no last_error (list responses omit it); full notes do.
const ERROR_PREVIEW = 40

const text = computed(() => {
  const { step, status } = props.processing
  if (step === 'none') return null
  if (status === 'failed') {
    const err = props.processing.last_error
    if (!err) return 'failed'
    return `failed: ${err.length > ERROR_PREVIEW ? `${err.slice(0, ERROR_PREVIEW)}…` : err}`
  }
  if (props.sourceKind === 'audio' && step === 'transcribe') {
    if (status === 'pending') return 'queued'
    if (status === 'running') return 'transcribing'
  }
  if (props.sourceKind === 'text' && step === 'tag' && status === 'pending') {
    return 'queued for tagging'
  }
  return `${step} ${status}`
})

const muted = computed(
  () =>
    props.sourceKind === 'text' &&
    props.processing.step === 'tag' &&
    props.processing.status === 'pending',
)

const title = computed(() => {
  const err = props.processing.last_error
  return props.processing.status === 'failed' && err ? `failed: ${err}` : undefined
})
</script>

<template>
  <span
    v-if="text"
    data-testid="processing-badge"
    :class="{ 'badge-muted': muted }"
    :title="title"
    >{{ text }}</span
  >
</template>

<style scoped>
.badge-muted {
  color: var(--el-text-color-secondary);
}
</style>

<script setup lang="ts">
// Phase-1 processing truth (FRONTEND-DESIGN.md 5): text notes park at
// tag/pending forever, so that combination reads as a resting "queued for
// tagging" rather than a spinner; anything else shows raw step + status.
import { computed } from 'vue'

import type { ProcStep, SourceKind } from '@/api/types'

const props = defineProps<{
  sourceKind: SourceKind
  processing: { step: ProcStep; status: string }
}>()

const text = computed(() => {
  if (props.processing.step === 'none') return null
  if (props.sourceKind === 'text' && props.processing.step === 'tag' && props.processing.status === 'pending') {
    return 'queued for tagging'
  }
  return `${props.processing.step} ${props.processing.status}`
})
</script>

<template>
  <span v-if="text" data-testid="processing-badge">{{ text }}</span>
</template>

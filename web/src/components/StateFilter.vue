<script setup lang="ts">
// State dropdown (card web-2 slice 4): All plus the six wire states.
// Presentational only — the composable owns what a filter change means.
import { ElOption, ElSelect } from 'element-plus'

import type { State } from '@/api/types'

defineProps<{ modelValue: State | '' }>()
const emit = defineEmits<{ 'update:modelValue': [value: State | ''] }>()

const states: State[] = ['inbox', 'todo', 'in_progress', 'done', 'someday', 'archived']
</script>

<template>
  <ElSelect
    data-testid="state-filter"
    :model-value="modelValue"
    @update:model-value="emit('update:modelValue', $event as State | '')"
  >
    <ElOption label="All" value="" />
    <ElOption v-for="state in states" :key="state" :label="state" :value="state" />
  </ElSelect>
</template>

<script setup lang="ts">
// Debounced search box (card web-2 slice 4). The component owns the 300 ms
// window; the list only sees settled queries. Escape blurs and keeps the
// text: a second Escape lands on body and the list's Escape binding clears q
// (FRONTEND-DESIGN.md 7).
import { ElInput } from 'element-plus'
import { onBeforeUnmount, ref, watch } from 'vue'

const props = defineProps<{ modelValue: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()

const text = ref(props.modelValue)
let timer: ReturnType<typeof setTimeout> | undefined

// External writes (Escape clear) must overwrite what the user typed.
watch(
  () => props.modelValue,
  (value) => {
    if (value !== text.value) {
      clearTimeout(timer)
      text.value = value
    }
  },
)

function onInput(value: string): void {
  text.value = value
  clearTimeout(timer)
  timer = setTimeout(() => emit('update:modelValue', value), 300)
}

onBeforeUnmount(() => clearTimeout(timer))

function onEsc(): void {
  const el = document.activeElement
  if (el instanceof HTMLElement) el.blur()
}
</script>

<template>
  <ElInput
    data-testid="search-input"
    placeholder="search notes"
    :model-value="text"
    clearable
    @update:model-value="onInput"
    @keydown.esc="onEsc"
  />
</template>

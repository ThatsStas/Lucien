<script setup lang="ts">
// Quick-create dialog (card web-2 slice 4). It calls createText itself —
// success closes it and emits created so the list can refetch immediately.
// While open it locks the keymap, which keeps j/k/n inert and leaves Escape
// to ElDialog's own handling (FRONTEND-DESIGN.md 7).
import { ElButton, ElDialog, ElInput, ElMessage } from 'element-plus'
import { onBeforeUnmount, ref, watch } from 'vue'

import { useAppApi } from '@/api/app-api'
import { lockDialogs, unlockDialogs } from '@/lib/keymap'

const props = defineProps<{ visible: boolean }>()
const emit = defineEmits<{ 'update:visible': [value: boolean]; created: [] }>()

const api = useAppApi()
const text = ref('')
const submitting = ref(false)

let locked = false
function setOpen(open: boolean): void {
  if (open && !locked) {
    lockDialogs()
    locked = true
  } else if (!open && locked) {
    unlockDialogs()
    locked = false
  }
}
watch(() => props.visible, setOpen, { immediate: true })
onBeforeUnmount(() => setOpen(false))

async function submit(): Promise<void> {
  const value = text.value.trim()
  if (!value || submitting.value) return
  submitting.value = true
  try {
    await api.createText({ text: value })
    text.value = ''
    emit('created')
    emit('update:visible', false)
    ElMessage({ message: 'note created', type: 'success' })
  } catch (err) {
    ElMessage({ message: err instanceof Error ? err.message : 'create failed', type: 'error' })
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <ElDialog
    :model-value="visible"
    title="New note"
    @update:model-value="emit('update:visible', $event)"
  >
    <ElInput
      data-testid="new-note-text"
      type="textarea"
      :rows="4"
      placeholder="What is on your mind?"
      :model-value="text"
      @update:model-value="text = $event"
    />
    <template #footer>
      <ElButton data-testid="new-note-submit" type="primary" :loading="submitting" :disabled="!text.trim()" @click="submit">
        Add
      </ElButton>
    </template>
  </ElDialog>
</template>

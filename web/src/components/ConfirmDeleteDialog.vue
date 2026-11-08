<script setup lang="ts">
// Shared delete confirmation (card web-3 slice 3). Chrome only: the caller
// (detail header or list `d`) runs deleteNote itself, so a 404 can be handled
// per context. The button names the id: "delete 01ABC…", never a bare "OK".
// While open it locks the keymap, leaving Escape to ElDialog (section 7).
import { ElButton, ElDialog } from 'element-plus'
import { onBeforeUnmount, watch } from 'vue'

import { lockDialogs, unlockDialogs } from '@/lib/keymap'

const props = defineProps<{ visible: boolean; noteId: string }>()
const emit = defineEmits<{ 'update:visible': [value: boolean]; confirm: [] }>()

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

function close(): void {
  emit('update:visible', false)
}
</script>

<template>
  <ElDialog :model-value="visible" title="Delete note" width="24rem" @update:model-value="close">
    <p>
      Delete <code>{{ noteId }}</code>? The note moves to the trash with its
      audio; nothing is destroyed yet.
    </p>
    <template #footer>
      <ElButton data-testid="confirm-cancel" @click="close">Cancel</ElButton>
      <ElButton data-testid="confirm-delete" type="danger" @click="emit('confirm')">
        delete {{ noteId }}
      </ElButton>
    </template>
  </ElDialog>
</template>

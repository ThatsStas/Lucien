<script setup lang="ts">
// One row of the note list (card web-2 slice 2, FRONTEND-DESIGN.md 8). The
// snippet is already server-capped at 200 chars; the client never re-truncates.
import { ElTag } from 'element-plus'

import ProcessingBadge from './ProcessingBadge.vue'
import { relativeTime } from '@/lib/time'
import type { Summary } from '@/api/types'

defineProps<{
  note: Summary
  selected: boolean
}>()

const emit = defineEmits<{ select: [] }>()
</script>

<template>
  <div
    :data-testid="`note-row-${note.id}`"
    role="listitem"
    :aria-current="selected ? 'true' : undefined"
    class="note-row"
    @click="emit('select')"
  >
    <span class="row-state">{{ note.state }}</span>
    <span class="row-tags">
      <ElTag v-for="tag in note.tags" :key="tag.name" size="small">{{ tag.name }}</ElTag>
    </span>
    <span class="row-source">{{ note.source.kind }}</span>
    <span class="row-snippet">{{ note.snippet }}</span>
    <ProcessingBadge :source-kind="note.source.kind" :processing="note.processing" />
    <time class="row-modified" :datetime="note.modified">{{ relativeTime(note.modified) }}</time>
  </div>
</template>

<style scoped>
.note-row {
  display: flex;
  gap: 0.75rem;
  align-items: baseline;
  padding: 0.4rem 0.5rem;
  cursor: pointer;
}
.note-row[aria-current='true'] {
  background: var(--el-color-primary-light-9);
}
.row-tags {
  display: inline-flex;
  gap: 0.25rem;
}
.row-snippet {
  flex: 1;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}
.row-modified {
  color: var(--el-text-color-secondary);
  font-size: 0.85em;
}
</style>

<script setup lang="ts">
// The 409 side-by-side (card web-4 slice 3). Left: the server note exactly
// as the 409 returned it — the winner the loser must admit it saw. Right:
// the patch that was about to go out. Chrome only: both buttons emit, the
// caller resends or adopts. While open it locks the keymap, leaving Escape
// to ElDialog (section 7); Esc closes without choosing, keeping the draft.
import { ElButton, ElDialog, ElTag } from 'element-plus'
import { onBeforeUnmount, watch } from 'vue'

import { lockDialogs, unlockDialogs } from '@/lib/keymap'
import type { ConflictDraft, Note } from '@/api/types'

const props = defineProps<{ visible: boolean; server: Note; draft: ConflictDraft }>()
const emit = defineEmits<{ 'take-server': []; 'use-mine': []; close: [] }>()

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
</script>

<template>
  <ElDialog
    :model-value="visible"
    title="The note changed under you"
    width="46rem"
    destroy-on-close
    @update:model-value="emit('close')"
  >
    <div data-testid="conflict-dialog" class="conflict-cols">
      <section data-testid="conflict-server" class="conflict-col">
        <h4>Server · <code>{{ server.modified }}</code></h4>
        <p class="conflict-meta">state {{ server.state }} · priority {{ server.priority }}</p>
        <ul class="conflict-entries">
          <li v-for="entry in server.entries" :key="entry.id" data-testid="server-entry">
            <span class="conflict-entry-id">{{ entry.id }} {{ entry.kind }}</span>
            <pre class="pre-wrap">{{ entry.text }}</pre>
          </li>
        </ul>
        <p class="conflict-tags">
          <ElTag
            v-for="tag in server.tags"
            :key="tag.name"
            size="small"
            :type="tag.by === 'agent' ? 'info' : 'primary'"
          >
            {{ tag.name }}
          </ElTag>
        </p>
      </section>
      <section data-testid="conflict-draft" class="conflict-col">
        <h4>Your draft</h4>
        <p v-if="draft.state" class="conflict-meta">state {{ draft.state }}</p>
        <div v-if="draft.entry_edit" data-testid="draft-entry" class="conflict-entry">
          <span class="conflict-entry-id">entry {{ draft.entry_edit.id }}</span>
          <pre class="pre-wrap">{{ draft.entry_edit.text }}</pre>
        </div>
        <p v-if="draft.priority !== undefined" class="conflict-meta">
          priority {{ draft.priority }}
        </p>
        <p v-if="draft.tags" class="conflict-tags">
          <ElTag
            v-for="tag in draft.tags"
            :key="tag.name"
            size="small"
            :type="tag.by === 'agent' ? 'info' : 'primary'"
          >
            {{ tag.name }}
          </ElTag>
        </p>
      </section>
    </div>
    <template #footer>
      <ElButton data-testid="conflict-take-server" @click="emit('take-server')">
        Take server
      </ElButton>
      <ElButton data-testid="conflict-use-mine" type="primary" @click="emit('use-mine')">
        Use mine
      </ElButton>
    </template>
  </ElDialog>
</template>

<style scoped>
.conflict-cols {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 1rem;
}
.conflict-col {
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 6px;
  padding: 0.75rem;
  min-width: 0;
}
.conflict-col h4 {
  margin: 0 0 0.5rem;
  font-size: 0.9rem;
}
.conflict-meta {
  margin: 0.25rem 0;
  font-size: 0.8rem;
  color: var(--el-text-color-secondary);
}
.conflict-entries {
  list-style: none;
  margin: 0.25rem 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
}
.conflict-entry,
.conflict-entries li {
  display: flex;
  flex-direction: column;
  gap: 0.15rem;
}
.conflict-entry-id {
  font-size: 0.7rem;
  text-transform: uppercase;
  color: var(--el-text-color-secondary);
}
.conflict-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 0.25rem;
  margin: 0.4rem 0 0;
}
.pre-wrap {
  white-space: pre-wrap;
  word-break: break-word;
  font: inherit;
  margin: 0;
}
</style>

<script setup lang="ts">
// The list screen (card web-2 slice 4): composable + presentational pieces.
// Card 1's placeholder probe is gone; the composable's mount fetch is the
// authenticated call the 401 -> toast -> /settings e2e rule exercises.
import { ElButton } from 'element-plus'
import { ref } from 'vue'

import NewNoteDialog from '@/components/NewNoteDialog.vue'
import NoteList from '@/components/NoteList.vue'
import SearchInput from '@/components/SearchInput.vue'
import StateFilter from '@/components/StateFilter.vue'
import { useAppApi } from '@/api/app-api'
import { useNoteList } from '@/composables/useNoteList'
import { useKeys } from '@/lib/keymap'
import { useSettingsStore } from '@/stores/settings'

const settings = useSettingsStore()
const list = useNoteList(useAppApi(), { getLimit: () => settings.pageSize })

const dialogVisible = ref(false)
const search = ref<InstanceType<typeof SearchInput> | null>(null)

function focusSearch(): void {
  const input = search.value?.$el?.querySelector('input')
  input?.focus()
}

// j/k clamp at the ends inside the composable. Escape here is the second
// press of the search clear gesture: the first one blurred the input and
// stopped at its own handler (FRONTEND-DESIGN.md 7).
useKeys({
  j: list.moveDown,
  k: list.moveUp,
  n: () => {
    dialogVisible.value = true
  },
  '/': focusSearch,
  Escape: () => {
    if (list.q.value) list.q.value = ''
  },
})
</script>

<template>
  <main>
    <h1>Notes</h1>
    <div class="list-toolbar">
      <SearchInput
        ref="search"
        :model-value="list.q.value"
        @update:model-value="list.q.value = $event"
      />
      <StateFilter
        :model-value="list.filters.state"
        @update:model-value="list.filters.state = $event"
      />
      <ElButton data-testid="new-note-button" type="primary" @click="dialogVisible = true">
        New note
      </ElButton>
    </div>
    <NoteList
      :rows="list.rows.value"
      :selected-index="list.selected.value"
      :loading="list.loading.value"
      :offline="list.offline.value"
      :showing-line="list.showingLine.value"
      @select="list.selectIndex"
    />
    <NewNoteDialog v-model:visible="dialogVisible" @created="list.refresh()" />
  </main>
</template>

<style scoped>
.list-toolbar {
  display: flex;
  gap: 0.5rem;
  align-items: center;
  margin-bottom: 0.75rem;
}
.list-toolbar :deep(.el-input) {
  max-width: 22rem;
}
</style>

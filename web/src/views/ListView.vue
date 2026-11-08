<script setup lang="ts">
// The list screen (card web-2 slice 4): composable + presentational pieces.
// Card 1's placeholder probe is gone; the composable's mount fetch is the
// authenticated call the 401 -> toast -> /settings e2e rule exercises.
import { ElButton, ElMessage } from 'element-plus'
import { ref } from 'vue'
import { useRouter } from 'vue-router'

import NewNoteDialog from '@/components/NewNoteDialog.vue'
import NoteList from '@/components/NoteList.vue'
import SearchInput from '@/components/SearchInput.vue'
import StateFilter from '@/components/StateFilter.vue'
import ConfirmDeleteDialog from '@/components/ConfirmDeleteDialog.vue'
import { useAppApi } from '@/api/app-api'
import { ApiError } from '@/api/client'
import { useNoteList } from '@/composables/useNoteList'
import { useKeys } from '@/lib/keymap'
import { useSettingsStore } from '@/stores/settings'

const settings = useSettingsStore()
const api = useAppApi()
const list = useNoteList(api, { getLimit: () => settings.pageSize })
const router = useRouter()

const dialogVisible = ref(false)
const search = ref<InstanceType<typeof SearchInput> | null>(null)

function focusSearch(): void {
  const input = search.value?.$el?.querySelector('input')
  input?.focus()
}

// Enter and row-click share one path: Enter has a click target (section 7).
function openIndex(index: number): void {
  const row = list.rows.value[index]
  if (row) router.push(`/n/${row.id}`)
}

// e opens the editor for the selected note: same navigation, plus the flag
// NoteView reads to start in edit mode (card web-4 slice 1).
function editIndex(index: number): void {
  const row = list.rows.value[index]
  if (row) router.push(`/n/${row.id}?edit=1`)
}

// d opens the shared confirm dialog for the selected row; the row disappears
// locally on success — the poll never resurrects a deleted note. A 404 is
// the same local removal with an info toast (card web-3 slice 3).
const deleteVisible = ref(false)
const deleteId = ref('')

function askDelete(): void {
  const row = list.rows.value[list.selected.value]
  if (row) {
    deleteId.value = row.id
    deleteVisible.value = true
  }
}

async function confirmDelete(): Promise<void> {
  const id = deleteId.value
  deleteVisible.value = false
  try {
    await api.deleteNote(id)
    list.removeRow(id)
    ElMessage({ message: `deleted ${id}`, type: 'success' })
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) {
      list.removeRow(id)
      ElMessage({ message: `deleted ${id}`, type: 'info' })
    } else {
      ElMessage({ message: 'delete failed', type: 'error' })
    }
  }
}

// j/k clamp at the ends inside the composable. Escape here is the second
// press of the search clear gesture: the first one blurred the input and
// stopped at its own handler (FRONTEND-DESIGN.md 7).
useKeys({
  j: list.moveDown,
  k: list.moveUp,
  Enter: () => openIndex(list.selected.value),
  n: () => {
    dialogVisible.value = true
  },
  d: askDelete,
  e: () => editIndex(list.selected.value),
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
      @open="openIndex"
    />
    <NewNoteDialog v-model:visible="dialogVisible" @created="list.refresh()" />
    <ConfirmDeleteDialog
      v-model:visible="deleteVisible"
      :note-id="deleteId"
      @confirm="confirmDelete"
    />
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

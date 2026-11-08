<script setup lang="ts">
// Deep link /n/:id (card web-3 slice 1). Fetches the full document once per
// id through the injected Api — live polling is card 5's usePollNote, Reload
// is the refresh until then. The Api arrives as a prop so unit tests mount
// without a network.

import { onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  ElButton,
  ElInput,
  ElInputNumber,
  ElMessage,
  ElOption,
  ElSelect,
} from 'element-plus'

import { useAppApi } from '@/api/app-api'
import { ApiError, type Api } from '@/api/client'
import type { ConflictDraft, Note, State, Tag } from '@/api/types'
import { useKeys } from '@/lib/keymap'
import ConflictDialog from '@/components/ConflictDialog.vue'
import ConfirmDeleteDialog from '@/components/ConfirmDeleteDialog.vue'
import TagEditor from '@/components/TagEditor.vue'

const STATES: State[] = ['inbox', 'todo', 'in_progress', 'done', 'someday', 'archived']

const props = defineProps<{ id: string; api?: Api }>()
const api = props.api ?? useAppApi()
const router = useRouter()
const route = useRoute()

const note = ref<Note | null>(null)
const notFound = ref(false)
const error = ref<string | null>(null)
const saving = ref(false)
// Select bound to a draft: on a failed patch the draft snaps back to the
// server truth held in note, visibly correcting the control.
const stateDraft = ref<State>('inbox')
// The 409 flow (card web-4 slice 3): the server note that refused our patch,
// the draft we were about to send, and the dialog that shows both. One
// surface for every patchNote conflict — editor saves and state changes
// ride the same code.
const conflictServer = ref<Note | null>(null)
const conflictDraft = ref<ConflictDraft>({})
const conflictVisible = ref(false)

function openConflict(server: Note, draft: ConflictDraft): void {
  conflictServer.value = server
  conflictDraft.value = draft
  conflictVisible.value = true
}

function closeConflict(): void {
  conflictVisible.value = false
}

async function load(): Promise<void> {
  notFound.value = false
  error.value = null
  conflictServer.value = null
  conflictVisible.value = false
  try {
    note.value = await api.getNote(props.id)
    if (note.value) stateDraft.value = note.value.state
  } catch (err) {
    note.value = null
    // The binary answers 400 for malformed ids and 404 for unknown ones;
    // both mean "nothing to show" (card web-3 acceptance 6).
    if (err instanceof ApiError && (err.status === 404 || err.status === 400)) {
      notFound.value = true
    } else if (err instanceof ApiError) {
      error.value = err.message
    } else {
      error.value = 'could not reach the server'
    }
  }
}

// base_modified is the last modified we read, kept as the unparsed string
// (FRONTEND-DESIGN.md 5): on ok the response is the only trusted source for
// the new modified.
async function changeState(next: State): Promise<void> {
  if (!note.value) return
  if (next === note.value.state) return
  saving.value = true
  try {
    const result = await api.patchNote(props.id, {
      base_modified: note.value.modified,
      state: next,
    })
    if (result.kind === 'ok') {
      note.value = result.note
      stateDraft.value = result.note.state
    } else {
      // Same dialog as the editor path (card web-4: state changes ride the
      // same patchNote and get it for free).
      openConflict(result.note, { state: next })
      stateDraft.value = note.value.state
    }
  } catch {
    ElMessage({ type: 'error', message: 'state change failed' })
    stateDraft.value = note.value.state
  } finally {
    saving.value = false
  }
}

// The router reuses this instance when only the id param changes; refetch
// rather than show the previous note, and drop any stale draft.
watch(
  () => props.id,
  () => {
    editing.value = false
    load()
  },
)

function back(): void {
  router.push('/')
}

// Edit mode (card web-4 slice 1). Drafts live only here: open seeds them
// from the note, save sends entry_edit + priority + tags in one patch with
// base_modified taken verbatim from the note's modified. The list's `e` key
// arrives as /n/<id>?edit=1.
const editing = ref(false)
const entryId = ref(0)
const entryText = ref('')
const priorityDraft = ref(0)
const tagsDraft = ref<Tag[]>([])

async function loadAndMaybeEdit(): Promise<void> {
  await load()
  editing.value = false
  if (route.query.edit && note.value) openEdit()
}
onMounted(loadAndMaybeEdit)

watch(entryId, (id) => {
  const entry = note.value?.entries.find((e) => e.id === id)
  if (entry) entryText.value = entry.text
})

function openEdit(): void {
  if (!note.value) return
  const first = note.value.entries[0]
  entryId.value = first?.id ?? 0
  entryText.value = first?.text ?? ''
  priorityDraft.value = note.value.priority
  tagsDraft.value = note.value.tags.map((t) => ({ ...t }))
  editing.value = true
}

function closeEdit(): void {
  editing.value = false
}

function toggleEdit(): void {
  if (editing.value) closeEdit()
  else openEdit()
}

async function saveEdit(): Promise<void> {
  if (!note.value || saving.value) return
  const draft: ConflictDraft = {
    entry_edit: { id: entryId.value, text: entryText.value },
    priority: priorityDraft.value,
    tags: tagsDraft.value,
  }
  saving.value = true
  try {
    const result = await api.patchNote(props.id, {
      base_modified: note.value.modified,
      ...draft,
    })
    if (result.kind === 'ok') {
      note.value = result.note
      stateDraft.value = result.note.state
      editing.value = false
      ElMessage({ message: 'saved', type: 'success' })
    } else {
      // Keep the draft on screen behind the dialog; the user decides there.
      openConflict(result.note, draft)
    }
  } catch {
    ElMessage({ message: 'save failed', type: 'error' })
  } finally {
    saving.value = false
  }
}

// Adopt the 409's note as local truth and re-seed any open editor from it
// (card web-4 acceptance 3): stay in edit mode, show the server's values.
function adoptServer(server: Note): void {
  note.value = server
  stateDraft.value = server.state
  if (editing.value) {
    const entry = server.entries.find((e) => e.id === entryId.value) ?? server.entries[0]
    entryId.value = entry?.id ?? 0
    entryText.value = entry?.text ?? ''
    priorityDraft.value = server.priority
    tagsDraft.value = server.tags.map((t) => ({ ...t }))
  }
}

function takeServer(): void {
  if (conflictServer.value) adoptServer(conflictServer.value)
  closeConflict()
  ElMessage({ message: 'kept the server version', type: 'info' })
}

// Last-writer-wins *after the loser admits it saw the winner*: resend the
// identical draft with the server's modified, never the stale base again.
// A third writer just reopens the dialog with fresh data — no retry loop.
async function useMine(): Promise<void> {
  const server = conflictServer.value
  if (!server || saving.value) return
  saving.value = true
  try {
    const result = await api.patchNote(props.id, {
      ...conflictDraft.value,
      base_modified: server.modified,
    })
    if (result.kind === 'ok') {
      note.value = result.note
      stateDraft.value = result.note.state
      editing.value = false
      closeConflict()
      ElMessage({ message: 'saved', type: 'success' })
    } else {
      conflictServer.value = result.note
    }
  } catch {
    closeConflict()
    ElMessage({ message: 'save failed', type: 'error' })
  } finally {
    saving.value = false
  }
}

useKeys({
  Escape: () => {
    // First Esc closes the editor; only a second one leaves the note.
    if (editing.value) closeEdit()
    else back()
  },
  e: toggleEdit,
})

// Delete (card web-3 slice 3): confirm dialog first, then deleteNote. A 404
// means someone already deleted it — same outcome, quieter toast. Other
// failures keep the note on screen.
const deleteVisible = ref(false)
async function confirmDelete(): Promise<void> {
  const id = props.id
  deleteVisible.value = false
  try {
    await api.deleteNote(id)
    router.push('/')
    ElMessage({ message: `deleted ${id}`, type: 'success' })
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) {
      router.push('/')
      ElMessage({ message: `deleted ${id}`, type: 'info' })
    } else {
      ElMessage({ message: 'delete failed', type: 'error' })
    }
  }
}
</script>

<template>
  <main class="note-view">
    <section v-if="notFound" data-testid="not-found" class="panel">
      <p>note not found</p>
      <a href="/" role="link" data-testid="not-found-back" @click.prevent="back">
        back to list
      </a>
    </section>
    <section v-else-if="error" data-testid="note-error" class="panel">
      <p>could not load the note: {{ error }}</p>
      <ElButton data-testid="reload-button" @click="load">Reload</ElButton>
    </section>
    <template v-else-if="note">
      <header class="note-header">
        <span data-testid="note-id" class="note-id">{{ note.id }}</span>
        <ElSelect
          v-model="stateDraft"
          data-testid="state-select"
          :disabled="saving"
          @change="changeState"
        >
          <ElOption v-for="s in STATES" :key="s" :label="s" :value="s" />
        </ElSelect>
        <span data-testid="note-source" class="note-source">{{ note.source.kind }}</span>
        <time data-testid="note-created" :datetime="note.created">{{ note.created }}</time>
        <time data-testid="note-modified" :datetime="note.modified">{{ note.modified }}</time>
        <ElButton data-testid="reload-button" size="small" @click="load">Reload</ElButton>
        <ElButton data-testid="back-button" size="small" @click="back">Back</ElButton>
        <ElButton data-testid="edit-toggle" size="small" @click="toggleEdit">
          {{ editing ? 'Close editor' : 'Edit' }}
        </ElButton>
        <ElButton data-testid="delete-button" size="small" type="danger" @click="deleteVisible = true">
          Delete
        </ElButton>
      </header>
      <section data-testid="processing-block" class="processing">
        <span data-testid="proc-step">{{ note.processing.step }}</span>
        <span data-testid="proc-status">{{ note.processing.status }}</span>
        <span v-if="note.processing.retry_count" class="proc-retries-label">
          retries <span data-testid="proc-retries">{{ note.processing.retry_count }}</span>
        </span>
        <pre v-if="note.processing.last_error" data-testid="proc-error" class="pre-wrap">
{{ note.processing.last_error }}</pre
        >
      </section>
      <section v-if="editing" data-testid="edit-form" class="edit-form">
        <ElSelect v-model="entryId" data-testid="entry-select" class="entry-picker">
          <ElOption
            v-for="entry in note.entries"
            :key="entry.id"
            :label="`${entry.id} ${entry.kind}`"
            :value="entry.id"
          />
        </ElSelect>
        <ElInput
          v-model="entryText"
          data-testid="entry-text"
          type="textarea"
          :rows="5"
          placeholder="Entry text"
        />
        <div class="edit-row">
          <span class="edit-label">Priority</span>
          <ElInputNumber v-model="priorityDraft" :min="0" :max="3" data-testid="priority-input" />
        </div>
        <TagEditor v-model="tagsDraft" />
        <div class="edit-actions">
          <ElButton data-testid="entry-save" type="primary" :disabled="saving" @click="saveEdit">
            {{ saving ? 'Saving…' : 'Save' }}
          </ElButton>
          <ElButton data-testid="edit-cancel" @click="closeEdit">Cancel</ElButton>
        </div>
      </section>
      <ol class="timeline">
        <li v-for="entry in note.entries" :key="entry.id" :data-testid="`entry-${entry.id}`">
          <span class="entry-kind">{{ entry.kind }}</span>
          <time :datetime="entry.ts">{{ entry.ts }}</time>
          <pre class="entry-text pre-wrap">{{ entry.text }}</pre>
        </li>
      </ol>
    </template>
    <p v-else data-testid="note-loading">loading…</p>
    <ConfirmDeleteDialog
      v-model:visible="deleteVisible"
      :note-id="note?.id ?? ''"
      @confirm="confirmDelete"
    />
    <ConflictDialog
      v-if="conflictVisible && conflictServer"
      :visible="conflictVisible"
      :server="conflictServer"
      :draft="conflictDraft"
      @take-server="takeServer"
      @use-mine="useMine"
      @close="closeConflict"
    />
  </main>
</template>

<style scoped>
.note-view {
  max-width: 46rem;
  margin: 0 auto;
  padding: 0.75rem 1rem;
}
.panel {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  padding: 1rem;
  border: 1px solid var(--el-border-color-light);
  border-radius: 6px;
}
.note-header {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  flex-wrap: wrap;
  font-size: 0.85rem;
  color: var(--el-text-color-regular);
}
.note-id {
  font-weight: 600;
}
.note-state,
.note-source {
  border: 1px solid var(--el-border-color-light);
  border-radius: 999px;
  padding: 0 0.5rem;
}
.processing {
  display: flex;
  align-items: baseline;
  gap: 0.5rem;
  margin: 0.5rem 0;
  font-size: 0.8rem;
  color: var(--el-text-color-secondary);
}
.pre-wrap {
  white-space: pre-wrap;
  word-break: break-word;
  font: inherit;
  margin: 0;
}
.timeline {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}
.timeline li {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
  padding: 0.5rem 0.75rem;
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 6px;
}
.entry-kind {
  font-size: 0.7rem;
  text-transform: uppercase;
  color: var(--el-text-color-secondary);
}
.timeline time {
  font-size: 0.7rem;
  color: var(--el-text-color-secondary);
}
.entry-text {
  color: var(--el-text-color-primary);
}
.edit-form {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  margin: 0.5rem 0;
  padding: 0.75rem;
  border: 1px solid var(--el-border-color);
  border-radius: 6px;
}
.edit-row {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}
.edit-label {
  font-size: 0.8rem;
  color: var(--el-text-color-secondary);
}
.edit-actions {
  display: flex;
  gap: 0.5rem;
}
.entry-picker {
  max-width: 12rem;
}
</style>

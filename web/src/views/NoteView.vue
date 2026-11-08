<script setup lang="ts">
// Deep link /n/:id (card web-3 slice 1). Fetches the full document once per
// id through the injected Api — live polling is card 5's usePollNote, Reload
// is the refresh until then. The Api arrives as a prop so unit tests mount
// without a network.

import { onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ElButton } from 'element-plus'

import { useAppApi } from '@/api/app-api'
import { ApiError, type Api } from '@/api/client'
import type { Note } from '@/api/types'

const props = defineProps<{ id: string; api?: Api }>()
const api = props.api ?? useAppApi()
const router = useRouter()

const note = ref<Note | null>(null)
const notFound = ref(false)
const error = ref<string | null>(null)

async function load(): Promise<void> {
  notFound.value = false
  error.value = null
  try {
    note.value = await api.getNote(props.id)
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

onMounted(load)
// The router reuses this instance when only the id param changes; refetch
// rather than show the previous note.
watch(() => props.id, load)

function back(): void {
  router.push('/')
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
        <span data-testid="note-state" class="note-state">{{ note.state }}</span>
        <span data-testid="note-source" class="note-source">{{ note.source.kind }}</span>
        <time data-testid="note-created" :datetime="note.created">{{ note.created }}</time>
        <time data-testid="note-modified" :datetime="note.modified">{{ note.modified }}</time>
        <ElButton data-testid="reload-button" size="small" @click="load">Reload</ElButton>
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
      <ol class="timeline">
        <li v-for="entry in note.entries" :key="entry.id" :data-testid="`entry-${entry.id}`">
          <span class="entry-kind">{{ entry.kind }}</span>
          <time :datetime="entry.ts">{{ entry.ts }}</time>
          <pre class="entry-text pre-wrap">{{ entry.text }}</pre>
        </li>
      </ol>
    </template>
    <p v-else data-testid="note-loading">loading…</p>
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
</style>

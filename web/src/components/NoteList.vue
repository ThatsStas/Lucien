<script setup lang="ts">
// Presentational list shell (card web-2 slice 2): selection lives in
// useNoteList, so this component only reports which row was clicked.
import NoteRow from './NoteRow.vue'
import type { Summary } from '@/api/types'

defineProps<{
  rows: Summary[]
  selectedIndex: number
  loading: boolean
  offline: boolean
  showingLine: string
}>()

const emit = defineEmits<{ select: [index: number] }>()
</script>

<template>
  <div>
    <p v-if="offline" data-testid="list-offline">offline</p>
    <div role="list" data-testid="note-list">
      <NoteRow
        v-for="(row, index) in rows"
        :key="row.id"
        :note="row"
        :selected="index === selectedIndex"
        @select="emit('select', index)"
      />
      <p v-if="!rows.length && !loading">no notes</p>
    </div>
    <p v-if="showingLine" data-testid="list-status">{{ showingLine }}</p>
  </div>
</template>

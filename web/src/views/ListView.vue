<script setup lang="ts">
// Placeholder for card 2's NoteList. The one-item probe is load-bearing:
// it proves the token works on app entry and gives the e2e suite a real
// authenticated call to exercise the 401 -> toast -> /settings rule.
import { onMounted, ref } from 'vue'

import { useAppApi } from '@/api/app-api'
import { ApiError } from '@/api/client'

const api = useAppApi()
const line = ref('loading…')

onMounted(async () => {
  try {
    const res = await api.listNotes({ limit: 1 })
    line.value = `list ready, ${res.total} notes`
  } catch (err) {
    line.value = err instanceof ApiError ? `error: ${err.message}` : 'error'
  }
})
</script>

<template>
  <main>
    <h1>Notes</h1>
    <p>{{ line }}</p>
  </main>
</template>

<script setup lang="ts">
import { onMounted } from 'vue'
import { RouterView } from 'vue-router'

import { useAppApi } from '@/api/app-api'
import { useBackendHealth } from '@/composables/useBackendHealth'
import { attachAppKeymap } from '@/lib/keymap'

const { state } = useBackendHealth(useAppApi())

// One global keydown listener for the whole app (FRONTEND-DESIGN.md 7);
// views feed it handler tables through useKeys.
onMounted(attachAppKeymap)
</script>

<template>
  <RouterView />
  <footer role="contentinfo" class="app-footer">
    {{ state === 'checking' ? 'checking backend…' : `backend ${state}` }}
  </footer>
</template>

<style scoped>
.app-footer {
  position: fixed;
  bottom: 0;
  left: 0;
  right: 0;
  padding: 0.35rem 0.75rem;
  font-size: 0.75rem;
  font-family: var(--el-font-family-mono, monospace);
  color: var(--el-text-color-secondary);
  background: var(--el-fill-color-lighter);
  border-top: 1px solid var(--el-border-color-lighter);
}
</style>

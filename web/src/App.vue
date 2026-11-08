<script setup lang="ts">
import { RouterView } from 'vue-router'

import { useAppApi } from '@/api/app-api'
import { useBackendHealth } from '@/composables/useBackendHealth'

const { state } = useBackendHealth(useAppApi())
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

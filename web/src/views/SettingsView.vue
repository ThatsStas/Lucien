<script setup lang="ts">
import { ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { ElButton, ElForm, ElFormItem, ElInput } from 'element-plus'

import { resetAuthHandler } from '@/api/app-api'
import { testConnection, type TestResult } from '@/api/test-connection'
import { useSettingsStore } from '@/stores/settings'

const settings = useSettingsStore()

const testing = ref(false)
const result = ref<TestResult | null>(null)

async function runTest() {
  testing.value = true
  try {
    result.value = await testConnection({
      token: settings.token,
      serverUrl: settings.serverUrl,
    })
  } finally {
    testing.value = false
  }
}

// A token edit means the user is fixing auth: allow the next 401 to alert
// again even while an older toast is still up.
watch(
  () => settings.token,
  () => resetAuthHandler(),
)
</script>

<template>
  <main class="settings">
    <h1>Settings</h1>
    <ElForm class="settings-form" @submit.prevent>
      <ElFormItem label="Bearer token">
        <ElInput
          v-model="settings.token"
          data-testid="settings-token"
          type="password"
          placeholder="token from the server config"
        />
      </ElFormItem>
      <ElFormItem label="Server URL (debug only)">
        <ElInput
          v-model="settings.serverUrl"
          data-testid="settings-server-url"
          placeholder="empty = same origin /v1"
        />
        <p class="hint">
          Leave empty for the normal same-origin setup. Only set it when debugging against another
          port.
        </p>
      </ElFormItem>
      <ElFormItem>
        <ElButton data-testid="settings-test" :loading="testing" @click="runTest">
          Test connection
        </ElButton>
      </ElFormItem>
    </ElForm>
    <p data-testid="settings-test-result" class="test-result">{{ result?.text ?? '' }}</p>
    <RouterLink to="/" data-testid="settings-back">Back to list</RouterLink>
  </main>
</template>

<style scoped>
.settings {
  max-width: 32rem;
  margin: 2rem auto;
  padding: 0 1rem;
}

.hint {
  margin: 0.25rem 0 0;
  font-size: 0.8rem;
  color: var(--el-text-color-secondary);
}

.test-result {
  min-height: 1.2rem;
  font-family: var(--el-font-family-mono, monospace);
  white-space: pre-wrap;
}
</style>

<script setup lang="ts">
// TagEditor (card web-4 slice 2): human tags are editable, agent tags are
// read-only chrome. Phase 2 overwrites agent tags wholesale, so this UI
// refuses to add or drop them; everything it emits keeps them verbatim.
// The parent owns the array; v-model in, replacement array out.
import { ElButton, ElInput, ElTag } from 'element-plus'
import { ref } from 'vue'

import type { Tag } from '@/api/types'

const props = defineProps<{ modelValue: Tag[] }>()
const emit = defineEmits<{ 'update:modelValue': [value: Tag[]] }>()

const draft = ref('')
const hint = ref('')

function removeTag(name: string): void {
  // Only ever reachable for by:"human" chips; the agent chip never renders
  // this button, so the emitted set keeps agent entries verbatim.
  emit(
    'update:modelValue',
    props.modelValue.filter((t) => t.name !== name),
  )
}

function addTag(): void {
  const name = draft.value.trim()
  if (!name) return
  const clash = props.modelValue.some((t) => t.name.toLowerCase() === name.toLowerCase())
  if (clash) {
    // The backend would happily accept the duplicate; we do not ship it.
    hint.value = `tag "${name}" is already on this note`
    return
  }
  hint.value = ''
  emit('update:modelValue', [...props.modelValue, { name, by: 'human' }])
  draft.value = ''
}

function clearHint(): void {
  hint.value = ''
}
</script>

<template>
  <div class="tag-editor">
    <div class="tag-chips">
      <ElTag
        v-for="tag in modelValue"
        :key="tag.name"
        :type="tag.by === 'agent' ? 'info' : 'primary'"
        :data-testid="`tag-chip-${tag.name}`"
        class="tag-chip"
      >
        <span class="tag-name">{{ tag.name }}</span>
        <span
          v-if="tag.by === 'agent'"
          :data-testid="`tag-lock-${tag.name}`"
          class="tag-lock"
          title="owned by the agent"
        >
          🔒
        </span>
        <button
          v-if="tag.by === 'human'"
          type="button"
          class="tag-remove"
          :data-testid="`tag-remove-${tag.name}`"
          :aria-label="`remove tag ${tag.name}`"
          @click="removeTag(tag.name)"
        >
          ×
        </button>
      </ElTag>
      <span v-if="!modelValue.length" class="tag-empty">no tags yet</span>
    </div>
    <div class="tag-entry">
      <ElInput
        v-model="draft"
        data-testid="tag-input"
        placeholder="add a tag"
        @keydown.enter="addTag"
        @input="clearHint"
      />
      <ElButton data-testid="tag-add" size="small" @click="addTag">Add</ElButton>
    </div>
    <p v-if="hint" data-testid="tag-hint" class="tag-hint">{{ hint }}</p>
  </div>
</template>

<style scoped>
.tag-editor {
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
}
.tag-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 0.35rem;
  align-items: center;
}
.tag-chip {
  display: inline-flex;
  align-items: center;
  gap: 0.25rem;
}
.tag-lock {
  color: var(--el-text-color-secondary);
}
.tag-remove {
  border: none;
  background: none;
  cursor: pointer;
  color: inherit;
  font-size: 0.9rem;
  line-height: 1;
  padding: 0 0.1rem;
}
.tag-entry {
  display: flex;
  gap: 0.4rem;
  max-width: 20rem;
}
.tag-empty {
  font-size: 0.8rem;
  color: var(--el-text-color-secondary);
}
.tag-hint {
  margin: 0;
  font-size: 0.8rem;
  color: var(--el-color-danger);
}
</style>

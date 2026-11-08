<script setup lang="ts">
// TagEditor (card web-4 slice 2): human tags are editable, agent tags are
// read-only chrome. Phase 2 overwrites agent tags wholesale, so this UI
// refuses to add or drop them; everything it emits keeps them verbatim.
// The parent owns the array; v-model in, replacement array out.
//
// Tag suggestions (post-card addendum): the vocabulary prop (the
// GET /v1/tags shape) drives a fuzzy-match dropdown under the input, so a
// human picks the existing spelling instead of minting home-todo vs
// "home todo". Typing an unpicked word still adds it verbatim — the
// dropdown is a nudge, never a gate. Arrow/Enter/Tab/Esc live here, on the
// input, because the global keymap is blind while any input is focused
// (FRONTEND-DESIGN.md 7); every suggestion is also a button.
import { ElButton, ElInput, ElTag } from 'element-plus'
import { computed, ref } from 'vue'

import type { Tag, TagCount } from '@/api/types'
import { fuzzyMatchTags } from '@/lib/tag-fuzzy'

const props = defineProps<{ modelValue: Tag[]; vocabulary?: TagCount[] }>()
const emit = defineEmits<{ 'update:modelValue': [value: Tag[]] }>()

const draft = ref('')
const hint = ref('')
// jsdom never fires focus events (even el.focus()), and in a browser the
// blur path can only hide the dropdown when focus left — after which the
// next focus reopens it. So "is the input the active typing context" is
// derived from keydown/click instead of focus/blur.
const engaged = ref(false)
// Esc closes the dropdown for good; typing reopens it.
const dismissed = ref(false)
const activeIndex = ref(-1)

const suggestions = computed(() => {
  if (!engaged.value || dismissed.value) return []
  return fuzzyMatchTags(
    draft.value,
    props.vocabulary ?? [],
    props.modelValue.map((t) => t.name),
  )
})

function removeTag(name: string): void {
  // Only ever reachable for by:"human" chips; the agent chip never renders
  // this button, so the emitted set keeps agent entries verbatim.
  emit(
    'update:modelValue',
    props.modelValue.filter((t) => t.name !== name),
  )
}

function addNamed(name: string): void {
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

function addTag(): void {
  addNamed(draft.value.trim())
}

function acceptSuggestion(name: string): void {
  addNamed(name)
  // Stay closed until typing resumes, even though the emptied draft would
  // otherwise re-show the most-used tags under a still-focused input.
  dismissed.value = true
  activeIndex.value = -1
}

function onInput(): void {
  hint.value = ''
  dismissed.value = false
  activeIndex.value = -1
}

// Any key or click on the input makes it the active typing context.
function engage(): void {
  engaged.value = true
  dismissed.value = false
}

// A real blur (focus left for good — the suggestion buttons preventDefault
// their mousedown, so picking one never blurs) retires the dropdown.
function onBlur(): void {
  engaged.value = false
  activeIndex.value = -1
}

function move(delta: number): void {
  const len = suggestions.value.length
  activeIndex.value = (activeIndex.value + delta + len) % len
}

// One keydown owner for the input: Enter either accepts the highlighted
// suggestion or adds the raw draft, never both. ElInput types the event as
// Event | KeyboardEvent; only keys we name are ever read.
function onKeydown(raw: Event): void {
  const ev = raw as KeyboardEvent
  engage()
  const items = suggestions.value
  if (ev.key === 'Enter') {
    if (items.length && activeIndex.value >= 0) {
      ev.preventDefault()
      acceptSuggestion(items[activeIndex.value]!.tag)
    } else {
      addTag()
    }
    return
  }
  if (!items.length) return
  if (ev.key === 'ArrowDown') {
    ev.preventDefault()
    move(1)
  } else if (ev.key === 'ArrowUp') {
    ev.preventDefault()
    move(-1)
  } else if (ev.key === 'Tab' && activeIndex.value >= 0) {
    ev.preventDefault() // accept instead of tabbing away mid-suggestion
    acceptSuggestion(items[activeIndex.value]!.tag)
  } else if (ev.key === 'Escape') {
    ev.preventDefault()
    dismissed.value = true
    activeIndex.value = -1
  }
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
    <div class="tag-entry-wrap">
      <div class="tag-entry">
        <ElInput
          v-model="draft"
          data-testid="tag-input"
          placeholder="add a tag"
          @keydown="onKeydown"
          @click="engage"
          @input="onInput"
          @blur="onBlur"
        />
        <ElButton data-testid="tag-add" size="small" @click="addTag">Add</ElButton>
      </div>
      <ul v-if="suggestions.length" data-testid="tag-suggest" class="tag-suggest">
        <li v-for="(s, i) in suggestions" :key="s.tag">
          <button
            type="button"
            :data-testid="`tag-suggest-${s.tag}`"
            :class="['tag-suggest-item', { 'is-active': i === activeIndex }]"
            @mousedown.prevent="acceptSuggestion(s.tag)"
          >
            {{ s.tag }}
          </button>
        </li>
      </ul>
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
.tag-entry-wrap {
  position: relative;
  max-width: 20rem;
}
.tag-entry {
  display: flex;
  gap: 0.4rem;
}
.tag-suggest {
  position: absolute;
  z-index: 5;
  top: calc(100% + 2px);
  left: 0;
  right: 0;
  margin: 0;
  padding: 0.2rem 0;
  list-style: none;
  display: flex;
  flex-direction: column;
  border: 1px solid var(--el-border-color);
  border-radius: 6px;
  background: var(--el-bg-color);
  box-shadow: var(--el-box-shadow-light);
}
.tag-suggest-item {
  display: block;
  width: 100%;
  text-align: left;
  border: none;
  background: none;
  cursor: pointer;
  padding: 0.25rem 0.6rem;
  font: inherit;
  color: inherit;
}
.tag-suggest-item:hover,
.tag-suggest-item.is-active {
  background: var(--el-fill-color-light);
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

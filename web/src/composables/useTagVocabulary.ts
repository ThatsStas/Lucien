// The tag vocabulary behind the editor's suggestions (GET /v1/tags): one
// fetch per app load, cached module-wide, deduped across concurrent openers.
// A failed fetch leaves the cache empty and typing works exactly as it did
// before suggestions existed — the dropdown is a nudge, never a blocker.

import { ref } from 'vue'

import { useAppApi } from '@/api/app-api'
import type { Api } from '@/api/client'
import type { TagCount } from '@/api/types'

const cache = ref<TagCount[]>([])
let inflight: Promise<void> | null = null

export function useTagVocabulary(injected?: Api) {
  async function load(): Promise<void> {
    if (cache.value.length > 0 || inflight) return
    inflight = (async () => {
      try {
        // Injected Api first (the unit-test seam every view already uses).
        const client = injected ?? useAppApi()
        const res = await client.listTags()
        cache.value = res.tags
      } catch {
        cache.value = []
      } finally {
        inflight = null
      }
    })()
    await inflight
  }
  return { vocabulary: cache, load }
}

// Test seam: the module cache outlives any single mount, so specs reset it
// explicitly instead of asserting against stale vocabulary.
export function __clearTagVocabularyCache(): void {
  cache.value = []
  inflight = null
}

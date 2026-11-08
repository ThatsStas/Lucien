// ProcessingBadge pins the five variants the card prescribes (web-3 slice 4):
// the muted phase-1 rest state, audio queued/transcribing, failed with a
// truncated message plus full-text title, and the raw step+status fallback.
// The rest state must never read as a spinner: text notes sit there forever.

import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import ProcessingBadge from '@/components/ProcessingBadge.vue'
import type { ProcStep, SourceKind } from '@/api/types'

function mountBadge(sourceKind: SourceKind, step: ProcStep, status: string, lastError: string | null = null) {
  return mount(ProcessingBadge, {
    props: {
      sourceKind,
      processing: { step, status, retry_count: 0, next_retry_at: null, last_error: lastError },
    },
  })
}

function badge(wrapper: ReturnType<typeof mountBadge>) {
  return wrapper.get('[data-testid="processing-badge"]')
}

describe('ProcessingBadge', () => {
  it('text note at rest reads "queued for tagging", muted, no spinner', () => {
    const wrapper = mountBadge('text', 'tag', 'pending')
    const el = badge(wrapper)
    expect(el.text()).toBe('queued for tagging')
    expect(el.classes()).toContain('badge-muted')
    expect(el.find('i, svg, [class*="spin"]').exists()).toBe(false)
  })

  it('audio waiting on whisper reads "queued"', () => {
    expect(badge(mountBadge('audio', 'transcribe', 'pending')).text()).toBe('queued')
  })

  it('audio mid-transcription reads "transcribing"', () => {
    expect(badge(mountBadge('audio', 'transcribe', 'running')).text()).toBe('transcribing')
  })

  it('failed shows a truncated last_error with the full text as title', () => {
    const err = 'whisper crashed while decoding segment 12 of 40: empty audio buffer'
    const wrapper = mountBadge('audio', 'transcribe', 'failed', err)
    const el = badge(wrapper)
    expect(el.text()).toBe(`failed: ${err.slice(0, 40)}…`)
    expect(el.attributes('title')).toBe(`failed: ${err}`)
  })

  it('failed with no recorded error still says failed', () => {
    expect(badge(mountBadge('audio', 'extract', 'failed')).text()).toBe('failed')
  })

  it('anything else falls back to the raw step and status', () => {
    expect(badge(mountBadge('audio', 'embed', 'running')).text()).toBe('embed running')
  })

  it('renders nothing when no step is active', () => {
    expect(mountBadge('text', 'none', 'pending').find('[data-testid="processing-badge"]').exists()).toBe(
      false,
    )
  })
})

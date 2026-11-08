// relativeTime is display-only math on a fixed clock (card web-2 slice 2).

import { describe, expect, it } from 'vitest'

import { relativeTime } from '@/lib/time'

const NOW = new Date('2026-11-08T12:00:00Z')

describe('relativeTime', () => {
  it('calls anything under a minute just now', () => {
    expect(relativeTime('2026-11-08T11:59:30Z', NOW)).toBe('just now')
  })

  it('shows minutes, hours and days up to a month', () => {
    expect(relativeTime('2026-11-08T11:55:00Z', NOW)).toBe('5m ago')
    expect(relativeTime('2026-11-08T09:00:00Z', NOW)).toBe('3h ago')
    expect(relativeTime('2026-10-30T12:00:00Z', NOW)).toBe('9d ago')
  })

  it('falls back to the plain date beyond a month', () => {
    expect(relativeTime('2026-08-01T12:00:00Z', NOW)).toBe('2026-08-01')
  })

  it('echoes an unparseable timestamp instead of crashing', () => {
    expect(relativeTime('not-a-date', NOW)).toBe('not-a-date')
  })
})

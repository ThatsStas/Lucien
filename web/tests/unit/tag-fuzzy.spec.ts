// The tag matcher behind the editor's suggestion dropdown (post-card
// addendum). Pure tier list: case and separators forgiven, one tier per
// confidence, ranked deterministically, capped. Every tier lives here.

import { describe, expect, it } from 'vitest'

import { fuzzyMatchTags, normalizeTag } from '@/lib/tag-fuzzy'
import type { TagCount } from '@/api/types'

function tc(tag: string, count = 1): TagCount {
  return { tag, count }
}

function names(result: TagCount[]): string[] {
  return result.map((x) => x.tag)
}

describe('normalizeTag', () => {
  it('folds case and separator runs to single spaces', () => {
    expect(normalizeTag('  Home_Todo. ')).toBe('home todo')
    expect(normalizeTag('a--b//c.')).toBe('a b c')
  })
})

describe('fuzzyMatchTags', () => {
  it('ranks exact over prefix over token-prefix over substring over subsequence', () => {
    const vocab = [
      tc('kernel panic'), // only reachable by the "knl" query below
      tc('wien'), // exact
      tc('wien-roast'), // prefix
      tc('old wien story'), // token prefix
      tc('mywien'), // substring
      tc('slow internet'), // subsequence: slowinternet hides w-i-e-n in order
    ]
    expect(names(fuzzyMatchTags('wien', vocab))).toEqual([
      'wien',
      'wien-roast',
      'old wien story',
      'mywien',
      'slow internet',
    ])
    expect(names(fuzzyMatchTags('knl', vocab))).toEqual(['kernel panic'])
  })

  it('forgives case and separator differences on both sides', () => {
    const vocab = [tc('Home_Todo'), tc('other')]
    expect(names(fuzzyMatchTags('  home TODO ', vocab))).toEqual(['Home_Todo'])
    expect(names(fuzzyMatchTags('home-todo', vocab))).toEqual(['Home_Todo'])
  })

  it('matches multi-word queries only when the words appear in order', () => {
    const vocab = [tc('home todo list'), tc('todo home'), tc('unrelated')]
    expect(names(fuzzyMatchTags('home todo', vocab))).toEqual(['home todo list'])
  })

  it('prefers the more-used tag among equal-tier matches', () => {
    // Both are prefix-tier (80) for "home todo": count breaks the tie.
    const vocab = [tc('home todo list', 5), tc('home-todo lister', 9)]
    expect(names(fuzzyMatchTags('home todo', vocab))).toEqual([
      'home-todo lister',
      'home todo list',
    ])
  })

  it('tags already on the note never surface, case and separators forgiven', () => {
    const vocab = [tc('Wien'), tc('kernel'), tc('groceries')]
    expect(names(fuzzyMatchTags('', vocab, ['wien']))).toEqual(['kernel', 'groceries'])
    expect(names(fuzzyMatchTags('w', vocab, ['  WIEN  ']))).toEqual([])
  })

  it('an empty query answers with the pool head — the vocabulary is already count-ordered', () => {
    const vocab = [tc('kernel', 9), tc('wien', 5), tc('zeta', 5), tc('alpha', 1)]
    expect(names(fuzzyMatchTags('', vocab))).toEqual(['kernel', 'wien', 'zeta', 'alpha'])
  })

  it('caps the list where a nudge stops being one', () => {
    const vocab = Array.from({ length: 12 }, (_, i) => tc(`todo-${i}`))
    expect(fuzzyMatchTags('todo', vocab)).toHaveLength(8)
    expect(fuzzyMatchTags('', vocab)).toHaveLength(8)
  })

  it('nonsense matches nothing', () => {
    const vocab = [tc('wien'), tc('kernel')]
    expect(fuzzyMatchTags('zzzq', vocab)).toEqual([])
  })
})

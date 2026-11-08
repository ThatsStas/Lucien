// The fuzzy half of the tag-suggestion dropdown. Deliberately the dumb
// analog of the backend's LIKE search (FRONTEND-DESIGN.md 2): case and
// separator runs (- _ . / space) are forgiven, one token of typing must
// appear in order inside the tag, and ranking is a fixed tier list. Pure
// function, no DOM and no fetch, so the unit spec covers every tier.

import type { TagCount } from '@/api/types'

// The dropdown is a nudge, not a catalogue.
const MAX_SUGGESTIONS = 8

// Lowercase, trimmed, separator runs collapsed to single spaces: the form
// both query and tag are compared in.
export function normalizeTag(value: string): string {
  return value
    .trim()
    .toLowerCase()
    .replace(/[\s\-_./]+/g, ' ')
    .trim()
}

// chars of needle appear in hay in order (not necessarily adjacent).
function subsequence(needle: string, hay: string): boolean {
  if (!needle) return true
  let i = 0
  for (const ch of hay) {
    if (ch === needle[i]) i += 1
    if (i === needle.length) return true
  }
  return false
}

// Every token of the query appears in hay, in order, non-overlapping.
function allInOrder(tokens: string[], hay: string): boolean {
  let from = 0
  for (const tok of tokens) {
    const at = hay.indexOf(tok, from)
    if (at < 0) return false
    from = at + tok.length
  }
  return true
}

// query arrives normalized and non-empty; higher wins, 0 means no match.
function score(query: string, tag: string): number {
  const t = normalizeTag(tag)
  if (!t) return 0
  if (t === query) return 100
  if (t.startsWith(query)) return 80
  if (t.split(' ').some((tok) => tok.startsWith(query))) return 70
  if (t.includes(query)) return 60
  const qTokens = query.split(' ')
  if (qTokens.length > 1 && allInOrder(qTokens, t)) return 50
  if (subsequence(query.replace(/ /g, ''), t.replace(/ /g, ''))) return 40
  return 0
}

// Ranked suggestions for the typed query. An empty query answers with the
// pool as given — the vocabulary arrives ordered count desc then tag asc,
// so that is "the most used tags". Tags in exclude (whatever the note
// already carries) never surface, case and separators forgiven.
export function fuzzyMatchTags(
  query: string,
  vocabulary: TagCount[],
  exclude: string[] = [],
): TagCount[] {
  const taken = new Set(exclude.map((name) => normalizeTag(name)))
  const pool = vocabulary.filter((tc) => !taken.has(normalizeTag(tc.tag)))
  const q = normalizeTag(query)
  if (!q) return pool.slice(0, MAX_SUGGESTIONS)
  return pool
    .map((tc) => ({ tc, s: score(q, tc.tag) }))
    .filter((x) => x.s > 0)
    .sort((a, b) => b.s - a.s || b.tc.count - a.tc.count || a.tc.tag.localeCompare(b.tc.tag))
    .slice(0, MAX_SUGGESTIONS)
    .map((x) => x.tc)
}

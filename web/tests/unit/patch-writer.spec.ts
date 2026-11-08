// Card web-4 DoD, enforced mechanically: exactly one code path sends PATCH.
// The Api is the only HTTP door (client.ts); among its writers, patchNote
// may be called from one view only, and base_modified may appear only where
// a caller forwards a note field, never a computed or defaulted timestamp.

import fs from 'node:fs'
import path from 'node:path'

import { describe, expect, it } from 'vitest'

const SRC = path.resolve(__dirname, '../../src')

function sources(dir: string = SRC): string[] {
  const out: string[] = []
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name)
    if (entry.isDirectory()) out.push(...sources(full))
    else if (/\.(ts|vue)$/.test(entry.name)) out.push(full)
  }
  return out
}

function rel(file: string): string {
  return path.relative(SRC, file)
}

describe('patch writer discipline', () => {
  const files = sources()

  it('patchNote is called only from client.ts and NoteView', () => {
    const callers = files
      .filter((f) => !rel(f).startsWith('api/'))
      .filter((f) => fs.readFileSync(f, 'utf8').includes('.patchNote('))
      .map(rel)
      .sort()
    expect(callers).toEqual(['views/NoteView.vue'])
  })

  it('base_modified appears only where a note field is forwarded', () => {
    const holders = files
      .filter((f) => fs.readFileSync(f, 'utf8').includes('base_modified'))
      .map(rel)
      .sort()
    // types.ts declares the field; the view forwards a modified read from a
    // Note. Nothing computes or defaults one. Slice 3 adds the conflict
    // dialog to this list, nothing else.
    expect(holders).toEqual(['api/types.ts', 'views/NoteView.vue'])
  })
})

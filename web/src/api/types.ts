// Wire types mirrored verbatim from the backend (FRONTEND-DESIGN.md 5).
// Field names are the on-disk JSON schema; change them only together with
// internal/notestore/note.go.

export type State = 'inbox' | 'todo' | 'in_progress' | 'done' | 'someday' | 'archived'
export type TagBy = 'agent' | 'human'
export type SourceKind = 'text' | 'audio' | 'link' | 'youtube' | 'ribbon'
export type EntryKind = 'text' | 'transcript' | 'excerpt'
export type ProcStep = 'transcribe' | 'extract' | 'tag' | 'embed' | 'none'
export type ProcStatus = 'pending' | 'running' | 'failed'

export interface Tag {
  name: string
  by: TagBy
}

export interface Source {
  kind: SourceKind
  url: string | null
}

export interface Entry {
  id: number
  ts: string
  kind: EntryKind
  text: string
}

export interface Processing {
  step: ProcStep
  status: ProcStatus
  retry_count: number
  next_retry_at: string | null
  last_error: string | null
}

export interface Note {
  id: string
  created: string
  modified: string
  state: State
  state_changed_at: string
  tags: Tag[]
  priority: number
  source: Source
  entries: Entry[]
  processing: Processing
  remind_at: string | null
  defer_until: string | null
  dedup: { url_hash: string | null; content_hash: string; resaved: string[] }
}

export interface Summary {
  id: string
  state: State
  tags: Tag[]
  priority: number
  created: string
  modified: string
  source: Source
  processing: { step: ProcStep; status: ProcStatus }
  snippet: string
}

export interface ListResult {
  notes: Summary[]
  total: number
}

export interface ListParams {
  state?: State[]
  tag?: string[]
  q?: string
  modified_since?: string
  limit?: number
  offset?: number
  processing_status?: ProcStatus
  remind_due?: boolean
  defer_due?: boolean
  min_age?: string
  max_age?: string
}

export interface CreateTextRequest {
  text: string
  state?: State
  priority?: number
  tags?: string[]
}

export interface CreateMeta {
  state?: State
  priority?: number
  tags?: string[]
}

// Timestamps travel as the exact RFC3339 strings the server gave us. Parsing
// to Date and reserializing can shift sub-second digits and manufacture a
// 409 (FRONTEND-DESIGN.md 5).
export interface Patch {
  base_modified: string
  state?: State
  tags?: Tag[]
  priority?: number
  remind_at?: string | null
  defer_until?: string | null
  entry_edit?: { id: number; text: string }
}

export type PatchResult = { kind: 'ok'; note: Note } | { kind: 'conflict'; note: Note }

// The body a save intended to send, minus base_modified: the conflict dialog
// shows it as "what you were about to send", and use-mine resends it with
// the server's modified (card web-4 slice 3). The sender supplies
// base_modified at send time, from a note it just read.
export type ConflictDraft = Omit<Patch, 'base_modified'>
